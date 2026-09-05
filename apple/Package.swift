// swift-tools-version: 5.9
import PackageDescription
let package = Package(name: "KageroCloud", platforms: [.macOS(.v13), .iOS("18.0")],
  products: [.library(name: "CloudHostKit", targets: ["CloudHostKit"]),
             .executable(name: "KageroCloud", targets: ["KageroCloud"])],
  targets: [.target(name: "CloudHostKit"),
            .executableTarget(name: "KageroCloud", dependencies: ["CloudHostKit"]),
            .testTarget(name: "CloudHostKitTests", dependencies: ["CloudHostKit"])])
