import CloudHostKit
import Foundation
import Darwin

enum LocalHost {
  static func call<T: Decodable>(_ command: String, input: Data? = nil) async throws -> T {
    let runner = HostProcess(command: command, input: input)
    return try await withTaskCancellationHandler {
      let data = try await runner.run()
      try Task.checkCancellation()
      return try JSONDecoder().decode(T.self, from: data)
    } onCancel: { runner.cancel() }
  }
}

private final class HostProcess: @unchecked Sendable {
  private let process = Process()
  private let lock = NSLock()
  private var cancelled = false
  private let command: String
  private let input: Data?
  init(command: String, input: Data?) { self.command = command; self.input = input }
  func cancel() {
    lock.lock(); cancelled = true
    if process.isRunning { process.terminate() }
    lock.unlock()
    DispatchQueue.global().asyncAfter(deadline: .now() + 1) { [self] in
      lock.lock(); defer { lock.unlock() }
      if process.isRunning { kill(process.processIdentifier, SIGKILL) }
    }
  }
  func run() async throws -> Data {
    try await withCheckedThrowingContinuation { continuation in
      DispatchQueue.global(qos: .utility).async {
        let output = Pipe(), stdin = Pipe()
        let base = FileManager.default.homeDirectoryForCurrentUser
          .appendingPathComponent("Library/Application Support/KageroHost/bin/kagero-host")
        self.process.executableURL = base
        self.process.arguments = [self.command]
        self.process.standardOutput = output; self.process.standardError = FileHandle.nullDevice; self.process.standardInput = stdin
        do {
          self.lock.lock()
          if self.cancelled { self.lock.unlock(); throw CancellationError() }
          do { try self.process.run() } catch { self.lock.unlock(); throw error }
          self.lock.unlock()
          let timeout = DispatchWorkItem { self.cancel() }
          DispatchQueue.global().asyncAfter(deadline: .now() + 12, execute: timeout)
          defer { timeout.cancel() }
          if let input = self.input { try stdin.fileHandleForWriting.write(contentsOf: input) }
          try stdin.fileHandleForWriting.close()
          // CLI bounds its response to 64 KiB and request lifetime to eight seconds.
          var data = Data()
          while let chunk = try output.fileHandleForReading.read(upToCount: 4096), !chunk.isEmpty {
            if data.count + chunk.count > 16384 { self.cancel(); throw CloudHostError.message("Host 响应过大") }
            data.append(chunk)
          }
          self.process.waitUntilExit()
          guard self.process.terminationStatus == 0 else {
            throw CloudHostError.message("Host 尚未就绪。请安装支持 iCloud 的新版 Host，并运行 kagero-host setup")
          }
          continuation.resume(returning: data)
        } catch { self.cancel(); continuation.resume(throwing: error) }
        try? output.fileHandleForReading.close()
        try? stdin.fileHandleForWriting.close()
      }
    }
  }
}
