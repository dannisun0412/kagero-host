import Foundation

public enum CloudHostError: LocalizedError {
  case message(String)
  public var errorDescription: String? { if case .message(let text) = self { return text }; return nil }
}

public struct CloudHost: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let name: String
  public let hostKey: String
  public let updatedAt: Int64
  public init(id: UUID, name: String, hostKey: String, updatedAt: Int64) {
    self.id = id; self.name = name; self.hostKey = hostKey; self.updatedAt = updatedAt
  }
  public func validate(now: Date = Date()) throws {
    guard validName(name), validSSHKey(hostKey), updatedAt > 0,
      updatedAt <= Int64(now.timeIntervalSince1970) + 300 else { throw CloudHostError.message("iCloud 电脑信息无效") }
  }
}

public struct CloudEnrollment: Codable, Sendable, Equatable, Identifiable {
  public let id: UUID
  public let hostID: UUID
  public let hostKey: String
  public let publicKey: String
  public let expiresAt: Int64
  public var invitation: String?
  public init(id: UUID = UUID(), host: CloudHost, publicKey: String, now: Date = Date()) {
    self.id = id; hostID = host.id; hostKey = host.hostKey; self.publicKey = publicKey
    expiresAt = Int64(now.timeIntervalSince1970) + 240
  }
  public func validate(now: Date = Date()) throws {
    let timestamp = Int64(now.timeIntervalSince1970)
    guard validSSHKey(hostKey), validSSHKey(publicKey), expiresAt > timestamp,
      expiresAt <= timestamp + 300, (invitation?.utf8.count ?? 0) <= 8192
    else { throw CloudHostError.message("iCloud 配对请求已过期或内容无效，请重新添加") }
  }
  public func matches(_ original: Self) -> Bool {
    id == original.id && hostID == original.hostID && hostKey == original.hostKey
      && publicKey == original.publicKey && expiresAt == original.expiresAt
  }
}

private func validName(_ value: String) -> Bool {
  !value.isEmpty && value.utf8.count <= 128
    && !value.unicodeScalars.contains { CharacterSet.controlCharacters.contains($0) }
}
private func validSSHKey(_ value: String) -> Bool {
  let parts = value.split(separator: " ")
  guard value.utf8.count <= 256, parts.count == 2, parts[0] == "ssh-ed25519",
    let data = Data(base64Encoded: String(parts[1])), data.count == 51 else { return false }
  return data.prefix(19) == Data([0, 0, 0, 11] + Array("ssh-ed25519".utf8) + [0, 0, 0, 32])
}
