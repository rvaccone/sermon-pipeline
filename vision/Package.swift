// swift-tools-version:5.10
import PackageDescription

let package = Package(
    name: "SermonVision",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "sermon-vision", path: "Sources/sermon-vision")
    ]
)
