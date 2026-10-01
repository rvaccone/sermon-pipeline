// sermon-vision: Apple Vision analysis for the sermon pipeline. Writes one JSON object per line
// to stdout. All boxes and points are normalized to 0...1 with the origin at the top-left.
//
//   sermon-vision pose  <video> --start S --end E [--rate 30]   people per frame, for tracking
//   sermon-vision faces <video> --start S --end E [--rate 0.5]  face quality + text, for thumbnails

import Foundation

let usage = """
    usage: sermon-vision pose  <video> --start SECONDS --end SECONDS [--rate FRAMES_PER_SECOND]
           sermon-vision faces <video> --start SECONDS --end SECONDS [--rate SAMPLES_PER_SECOND]
    """

do {
    let args = try Arguments(CommandLine.arguments.dropFirst())
    switch args.command {
    case "pose": try await analyzePose(args)
    case "faces": try await analyzeFaces(args)
    default: throw UsageError()
    }
} catch is UsageError {
    FileHandle.standardError.write(Data((usage + "\n").utf8))
    exit(2)
} catch {
    FileHandle.standardError.write(Data("sermon-vision: \(error)\n".utf8))
    exit(1)
}
