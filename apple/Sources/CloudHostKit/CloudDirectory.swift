@preconcurrency import CloudKit
@preconcurrency import Foundation
import Security

/// Private to the signed-in iCloud account. No public database or private key upload.
public actor CloudDirectory {
  public static let containerID = "iCloud.com.kageroai.terminalai"
  public static let changed = Notification.Name("KageroCloudDirectoryChanged")
  private var container: CKContainer { CKContainer(identifier: Self.containerID) }
  private var prepared = false
  private let zone = CKRecordZone.ID(zoneName: "KageroDevices", ownerName: CKCurrentUserDefaultName)
  private var database: CKDatabase { container.privateCloudDatabase }

  public init() {}
  public func accountChanged() { prepared = false }

  /// Opaque, container-scoped identity; never an email address or credential.
  public func accountIdentity() async throws -> String {
    try await withCheckedThrowingContinuation { continuation in
      let result = Completion<String>(continuation)
      let timer = Task {
        do { try await Task.sleep(for: .seconds(15)) } catch { return }
        result.finish(.failure(CloudHostError.message("iCloud 账户检查超时")))
      }
      container.fetchUserRecordID { id, error in
        timer.cancel()
        if let error { result.finish(.failure(error)) }
        else if let id { result.finish(.success(id.recordName)) }
        else { result.finish(.failure(CloudHostError.message("无法确认 iCloud 账户"))) }
      }
    }
  }

  public func prepare() async throws {
    if prepared { return }
    // Fail with actionable text instead of initializing CloudKit from an unsigned CLI.
    #if os(macOS)
    guard let task = SecTaskCreateFromSelf(nil),
      let ids = SecTaskCopyValueForEntitlement(task, "com.apple.developer.icloud-container-identifiers" as CFString, nil) as? [String],
      ids.contains(Self.containerID) else {
      throw CloudHostError.message("此版本尚未配置 iCloud 签名权限，请安装支持 iCloud 的签名版本")
    }
    #endif
    let status = try await accountStatus()
    guard status == .available else {
      throw CloudHostError.message(status == .noAccount ? "请先在系统设置登录 Apple 账号并启用 iCloud" : "iCloud 当前不可用，请检查网络或系统账户限制后重试")
    }
    let zones = CKModifyRecordZonesOperation(recordZonesToSave: [CKRecordZone(zoneID: zone)])
    try await perform(zones) { finish in zones.modifyRecordZonesResultBlock = finish }
    let sub = CKRecordZoneSubscription(zoneID: zone, subscriptionID: "kagero-devices-v1")
    let notification = CKSubscription.NotificationInfo(); notification.shouldSendContentAvailable = true
    sub.notificationInfo = notification
    let subscriptions = CKModifySubscriptionsOperation(subscriptionsToSave: [sub])
    try await perform(subscriptions) { finish in subscriptions.modifySubscriptionsResultBlock = finish }
    prepared = true
  }

  public func hosts() async throws -> [CloudHost] {
    try await allRecords().filter { $0.recordType == "KageroHost" }.map { record in
      let host: CloudHost = try decode(record); try host.validate()
      guard record.recordID.recordName == hostName(host.id) else { throw CloudHostError.message("iCloud 电脑编号不匹配") }
      return host
    }.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
  }

  /// Bounded lookup of a known computer; does not scan the entire account or create subscriptions.
  public func host(_ id: UUID) async throws -> CloudHost? {
    guard let record = try await fetch(CKRecord.ID(recordName: hostName(id), zoneID: zone), timeout: 3) else { return nil }
    let host: CloudHost = try decode(record); try host.validate()
    guard host.id == id else { throw CloudHostError.message("iCloud 电脑编号不匹配") }
    return host
  }

  public func publish(_ host: CloudHost) async throws {
    try host.validate()
    let id = CKRecord.ID(recordName: hostName(host.id), zoneID: zone)
    // Fetch change tag, so an account/host conflict cannot silently overwrite records.
    let record = try await fetch(id) ?? CKRecord(recordType: "KageroHost", recordID: id)
    record.encryptedValues["payload"] = try JSONEncoder().encode(host) as NSData
    try await save(record)
  }

  public func submit(_ request: CloudEnrollment) async throws {
    try request.validate()
    guard request.invitation == nil else { throw CloudHostError.message("不能提交已完成的配对请求") }
    let record = CKRecord(recordType: "KageroEnrollment", recordID: requestID(request.id))
    record.encryptedValues["payload"] = try JSONEncoder().encode(request) as NSData
    try await save(record)
  }

  public func response(to original: CloudEnrollment) async throws -> CloudEnrollment? {
    guard let record = try await fetch(requestID(original.id)) else { return nil }
    let request: CloudEnrollment = try decode(record)
    try request.validate()
    guard request.matches(original) else { throw CloudHostError.message("iCloud 配对身份已改变，请重新添加") }
    return request.invitation == nil ? nil : request
  }

  public func pending(for host: CloudHost) async throws -> [CloudEnrollment] {
    var result: [CloudEnrollment] = []
    for record in try await allRecords() where record.recordType == "KageroEnrollment" {
      let request: CloudEnrollment = try decode(record)
      guard request.hostID == host.id else { continue }
      if request.expiresAt <= Int64(Date().timeIntervalSince1970) {
        try await removeRequest(request.id); continue
      }
      try request.validate()
      guard record.recordID == requestID(request.id), request.hostKey == host.hostKey else {
        throw CloudHostError.message("iCloud 配对的电脑身份不匹配")
      }
      if request.invitation == nil { result.append(request) }
    }
    return result
  }

  public func respond(to original: CloudEnrollment, invitation: String) async throws {
    guard let record = try await fetch(requestID(original.id)) else { return }
    var current: CloudEnrollment = try decode(record)
    guard current.matches(original), current.invitation == nil else { return }
    current.invitation = invitation
    try current.validate()
    record.encryptedValues["payload"] = try JSONEncoder().encode(current) as NSData
    try await save(record)
  }

  public func removeRequest(_ id: UUID) async throws { try await remove(requestID(id)) }
  public func unpublish(_ id: UUID) async throws {
    for record in try await allRecords() where record.recordType == "KageroEnrollment" {
      let req: CloudEnrollment = try decode(record)
      if req.hostID == id { try await remove(record.recordID) }
    }
    try await remove(CKRecord.ID(recordName: hostName(id), zoneID: zone))
  }

  private func hostName(_ id: UUID) -> String { "host-" + id.uuidString.lowercased() }
  private func requestID(_ id: UUID) -> CKRecord.ID { CKRecord.ID(recordName: "request-" + id.uuidString.lowercased(), zoneID: zone) }
  private func decode<T: Decodable>(_ record: CKRecord) throws -> T {
    guard let data = record.encryptedValues["payload"] as? Data, data.count <= 16384 else {
      throw CloudHostError.message("iCloud 记录格式无效")
    }
    return try JSONDecoder().decode(T.self, from: data)
  }
  private func accountStatus() async throws -> CKAccountStatus {
    // CKContainer operations have no cancellable accountStatus variant. Bound the caller.
    try await withCheckedThrowingContinuation { continuation in
      let result = Completion<CKAccountStatus>(continuation)
      let timer = Task {
        do { try await Task.sleep(for: .seconds(15)) } catch { return }
        result.finish(.failure(CloudHostError.message("iCloud 账户检查超时")))
      }
      container.accountStatus { status, error in
        timer.cancel()
        result.finish(error.map { .failure($0) } ?? .success(status))
      }
    }
  }
  private func fetch(_ id: CKRecord.ID, timeout: TimeInterval = 22) async throws -> CKRecord? {
    let value = LockedRecords()
    let op = CKFetchRecordsOperation(recordIDs: [id])
    op.perRecordResultBlock = { _, result in value.add(result) }
    do { try await perform(op, timeout: timeout) { finish in op.fetchRecordsResultBlock = finish } }
    catch { if let e = value.error as? CKError, e.code == .unknownItem { return nil }; throw error }
    if let error = value.error { if (error as? CKError)?.code == .unknownItem { return nil }; throw error }
    return value.records.first
  }
  private func save(_ record: CKRecord) async throws {
    let op = CKModifyRecordsOperation(recordsToSave: [record]); op.savePolicy = .ifServerRecordUnchanged; op.isAtomic = true
    try await perform(op) { finish in op.modifyRecordsResultBlock = finish }
  }
  private func remove(_ id: CKRecord.ID) async throws {
    let op = CKModifyRecordsOperation(recordIDsToDelete: [id])
    do { try await perform(op) { finish in op.modifyRecordsResultBlock = finish } }
    catch let error as CKError where error.code == .unknownItem { return }
  }
  private func allRecords() async throws -> [CKRecord] {
    let values = LockedRecords()
    let options = CKFetchRecordZoneChangesOperation.ZoneConfiguration(); options.resultsLimit = 200
    let op = CKFetchRecordZoneChangesOperation(recordZoneIDs: [zone], configurationsByRecordZoneID: [zone: options])
    op.fetchAllChanges = true
    op.recordWasChangedBlock = { [weak op] _, result in
      values.add(result)
      if values.count > 256 { values.fail(CloudHostError.message("iCloud 设备记录过多，请清理旧配对后重试")); op?.cancel() }
    }
    op.recordZoneFetchResultBlock = { _, result in
      if case .failure(let error) = result { values.fail(error) }
    }
    op.recordWithIDWasDeletedBlock = { id, _ in values.delete(id) }
    try await perform(op) { finish in op.fetchRecordZoneChangesResultBlock = finish }
    if let error = values.error { throw error }
    return values.records
  }
  private func perform(_ op: CKDatabaseOperation, timeout: TimeInterval = 22, setup: (@escaping (Result<Void, Error>) -> Void) -> Void) async throws {
    op.configuration.timeoutIntervalForRequest = min(10, timeout)
    op.configuration.timeoutIntervalForResource = min(20, timeout)
    op.configuration.qualityOfService = .utility
    try await withTaskCancellationHandler {
      try Task.checkCancellation()
      try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
        let result = Completion(continuation)
        let timer = DispatchWorkItem { [weak op] in op?.cancel(); result.finish(.failure(CloudHostError.message("iCloud 同步超时，请检查网络后重试"))) }
        setup { value in timer.cancel(); result.finish(value) }
        DispatchQueue.global().asyncAfter(deadline: .now() + timeout, execute: timer)
        database.add(op)
      }
      try Task.checkCancellation()
    } onCancel: { op.cancel() }
  }
}

private final class Completion<T>: @unchecked Sendable {
  private let lock = NSLock()
  private var continuation: CheckedContinuation<T, Error>?
  init(_ value: CheckedContinuation<T, Error>) { continuation = value }
  func finish(_ result: Result<T, Error>) {
    lock.lock(); let value = continuation; continuation = nil; lock.unlock()
    value?.resume(with: result)
  }
}
private final class LockedRecords: @unchecked Sendable {
  private let lock = NSLock()
  private var storage: [CKRecord.ID: CKRecord] = [:]
  private var failure: Error?
  var count: Int { lock.lock(); defer { lock.unlock() }; return storage.count }
  var records: [CKRecord] { lock.lock(); defer { lock.unlock() }; return Array(storage.values) }
  var error: Error? { lock.lock(); defer { lock.unlock() }; return failure }
  func add(_ result: Result<CKRecord, Error>) {
    lock.lock(); defer { lock.unlock() }
    switch result { case .success(let record): if storage.count <= 256 { storage[record.recordID] = record }
    case .failure(let error): failure = error }
  }
  func fail(_ error: Error) { lock.lock(); failure = error; lock.unlock() }
  func delete(_ id: CKRecord.ID) { lock.lock(); storage[id] = nil; lock.unlock() }
}
