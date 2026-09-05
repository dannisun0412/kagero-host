import AppKit
import CloudKit
import CloudHostKit
import Network
import ServiceManagement
import SwiftUI

@main
struct KageroCloudApp: App {
  @NSApplicationDelegateAdaptor(CloudAppDelegate.self) private var delegate
  var body: some Scene {
    WindowGroup("Kagero · iCloud 连接", id: "settings") {
      CloudSettings(model: delegate.model).frame(width: 400).padding(24)
    }.windowResizability(.contentSize)
    MenuBarExtra("Kagero iCloud", systemImage: "terminal") {
      CloudMenu(model: delegate.model)
    }
  }
}
private struct CloudMenu: View {
  @ObservedObject var model: CloudCoordinator
  @Environment(\.openWindow) private var openWindow
  var body: some View {
    Text(model.status)
    Button("iCloud 连接设置") { openWindow(id: "settings"); NSApp.activate(ignoringOtherApps: true) }
    Button("立即同步") { model.refresh() }.disabled(!model.enabled)
    Divider()
    Button("退出") { NSApp.terminate(nil) }
  }
}
private struct CloudSettings: View {
  @ObservedObject var model: CloudCoordinator
  var body: some View {
    VStack(alignment: .leading, spacing: 16) {
      Text("在 iPhone 上发现这台 Mac").font(.title2.bold())
      Text("启用后，同一 Apple 账号下的 Kagero 可以发现并申请连接这台电脑。手机确认添加后，可访问终端和文件。私钥保留在各自设备的钥匙串。")
        .foregroundStyle(.secondary)
      Toggle("允许通过 iCloud 添加这台电脑", isOn: Binding(get: { model.enabled }, set: { model.setEnabled($0) }))
      Toggle("登录 Mac 时启动", isOn: Binding(get: { SMAppService.mainApp.status == .enabled }, set: { model.setLogin($0) }))
      Text(model.status).font(.callout).foregroundStyle(.secondary).textSelection(.enabled)
      if model.busy { ProgressView() }
      Button("立即同步") { model.refresh() }.disabled(!model.enabled || model.busy)
      Text("电脑需要开机联网。关闭此功能停止新设备发现，不撤销已配对设备；已配对设备可通过 kagero-host devices / revoke 管理。")
        .font(.caption).foregroundStyle(.secondary)
    }
  }
}
@MainActor final class CloudAppDelegate: NSObject, NSApplicationDelegate {
  let model = CloudCoordinator()
  private let network = NWPathMonitor()
  private var networkRefresh: Task<Void, Never>?
  private var accountObserver: NSObjectProtocol?
  func applicationDidFinishLaunching(_ notification: Notification) {
    NSApp.registerForRemoteNotifications(matching: [])
    accountObserver = NotificationCenter.default.addObserver(forName: .CKAccountChanged, object: nil, queue: .main) { [weak self] _ in
      Task { @MainActor in self?.model.accountChanged() }
    }
    network.pathUpdateHandler = { [weak self] path in
      Task { @MainActor in
        guard let self else { return }
        self.networkRefresh?.cancel()
        guard path.status == .satisfied else { return }
        self.networkRefresh = Task { [weak self] in
          do { try await Task.sleep(for: .milliseconds(500)) } catch { return }
          self?.model.refresh()
        }
      }
    }
    network.start(queue: DispatchQueue(label: "app.kagero.cloud.network"))
    model.refresh()
  }
  func applicationDidBecomeActive(_ notification: Notification) { model.refresh() }
  func application(_ application: NSApplication, didReceiveRemoteNotification userInfo: [String: Any]) {
    guard CKNotification(fromRemoteNotificationDictionary: userInfo)?.subscriptionID == "kagero-devices-v1" else { return }
    model.refresh()
  }
  func applicationWillTerminate(_ notification: Notification) {
    network.cancel(); networkRefresh?.cancel(); model.cancel()
    if let accountObserver { NotificationCenter.default.removeObserver(accountObserver) }
  }
}

@MainActor final class CloudCoordinator: ObservableObject {
  @Published private(set) var enabled = UserDefaults.standard.bool(forKey: "cloudEnabled")
  @Published private(set) var busy = false
  @Published var status = "尚未启用 iCloud 连接"
  private let directory = CloudDirectory()
  private var work: Task<Void, Never>?
  private var needsRefresh = false
  private var lastHost: CloudHost?
  private var generation = UUID()
  private var explicitConsent = false
  func accountChanged() {
    generation = UUID(); work?.cancel(); work = nil; busy = false; needsRefresh = false
    enabled = false; UserDefaults.standard.set(false, forKey: "cloudEnabled"); lastHost = nil
    explicitConsent = false
    UserDefaults.standard.removeObject(forKey: "cloudOwner")
    let current = generation
    status = "Apple 账号已改变，请重新启用 iCloud 连接"
    work = Task {
      await directory.accountChanged()
      do { let _: [String:Bool] = try await LocalHost.call("icloud-clear") }
      catch { if current == generation { status = "账户已改变，临时授权清理失败；授权会在几分钟内到期" } }
    }
  }
  func cancel() { work?.cancel() }
  func setLogin(_ enable: Bool) {
    do { if enable { try SMAppService.mainApp.register() } else { try SMAppService.mainApp.unregister() }; objectWillChange.send() }
    catch { status = "自动启动设置失败，请在系统设置的登录项中检查 Kagero" }
  }
  func setEnabled(_ value: Bool) {
    generation = UUID(); let current = generation
    enabled = value; UserDefaults.standard.set(value, forKey: "cloudEnabled")
    work?.cancel(); work = nil; busy = false
    explicitConsent = value
    if value { refresh() }
    else {
      status = "正在停止 iCloud 发现…"
      work = Task {
        do {
          let _: [String:Bool] = try await LocalHost.call("icloud-clear")
          let host: CloudHost
          if let lastHost { host = lastHost } else { host = try await LocalHost.call("icloud-host") }
          try await directory.prepare(); try await directory.unpublish(host.id)
          guard current == generation else { return }
          status = "已停止新设备发现；已有配对保留"
        } catch { if current == generation { status = "已关闭自动同步；临时授权或云端记录清理失败，请联网后重试关闭（临时授权几分钟内到期）" } }
      }
    }
  }
  func refresh() {
    guard enabled else { return }
    if busy { needsRefresh = true; return }
    busy = true
    let current = generation
    work = Task {
      defer { if current == generation { busy = false; work = nil; if needsRefresh { needsRefresh = false; refresh() } } }
      do {
        try await directory.prepare()
        let owner = try await directory.accountIdentity()
        try Task.checkCancellation(); guard current == generation, enabled else { return }
        if !explicitConsent && UserDefaults.standard.string(forKey: "cloudOwner") != owner {
          accountChanged(); return
        }
        UserDefaults.standard.set(owner, forKey: "cloudOwner"); explicitConsent = false
        let host: CloudHost = try await LocalHost.call("icloud-host")
        try Task.checkCancellation(); guard current == generation, enabled else { return }
        try host.validate(); lastHost = host
        // Publishing every push creates a notification feedback loop. Refresh the
        // discovery lease only on first observation or when older than one hour.
        let existing = try await directory.hosts().first { $0.id == host.id }
        if existing == nil || existing?.name != host.name || existing?.hostKey != host.hostKey
          || existing?.endpoints != host.endpoints || existing?.address != host.address || existing?.publicUDP != host.publicUDP
          || (existing?.updatedAt ?? 0) < host.updatedAt - 3600 { try await directory.publish(host) }
        for request in try await directory.pending(for: host) {
          try Task.checkCancellation(); guard current == generation, enabled else { return }
          struct Input: Encodable {
            let id: UUID; let hostID: UUID; let hostKey: String; let publicKey: String; let expiresAt: Int64
          }
          struct Reply: Decodable { let invitation: String }
          let input = Input(id: request.id, hostID: request.hostID, hostKey: request.hostKey, publicKey: request.publicKey, expiresAt: request.expiresAt)
          let reply: Reply = try await LocalHost.call("icloud-invite", input: JSONEncoder().encode(input))
          try Task.checkCancellation(); guard current == generation, enabled else { return }
          try await directory.respond(to: request, invitation: reply.invitation)
        }
        try Task.checkCancellation(); guard current == generation else { return }
        status = "已同步 \(host.name)，在同账号的 iPhone 上打开添加服务器"
      } catch is CancellationError { return }
      catch { if current == generation { status = "同步失败：\(error.localizedDescription)" } }
    }
  }
}
