import AVFoundation
import Vision

/// One analyzed frame: every candidate anchor for the person the camera should follow.
struct PoseRecord: Encodable {
    let t: Double
    let necks: [[Double]]   // [x, y] of each detected body's neck joint
    let faces: [[Double]]   // [x, y, w, h] face boxes
    let bodies: [[Double]]  // [x, y, w, h] full-body boxes
}

/// Reads every frame in the range in order and analyzes `rate` of them per second.
func analyzePose(_ args: Arguments) async throws {
    let rate = args.rate ?? 30
    let frames = try await FrameReader(url: args.video, from: args.start, to: args.end)
    let pose = VNDetectHumanBodyPoseRequest()
    let faces = VNDetectFaceRectanglesRequest()
    let bodies = VNDetectHumanRectanglesRequest()
    bodies.upperBodyOnly = false

    let output = JSONLines()
    var nextSample = args.start
    while let (time, pixels) = try frames.next() {
        guard time + 1e-6 >= nextSample else { continue }
        nextSample += 1 / rate
        try VNImageRequestHandler(cvPixelBuffer: pixels).perform([pose, faces, bodies])
        try output.write(PoseRecord(
            t: time,
            necks: (pose.results ?? []).compactMap { body in
                guard let neck = try? body.recognizedPoint(.neck), neck.confidence > 0.3 else { return nil }
                return point(neck.location)
            },
            faces: (faces.results ?? []).map { box($0.boundingBox) },
            bodies: (bodies.results ?? []).map { box($0.boundingBox) }
        ))
    }
}

/// Decodes frames sequentially, which is far faster than seeking to each one.
final class FrameReader {
    private let reader: AVAssetReader
    private let output: AVAssetReaderTrackOutput

    init(url: URL, from start: Double, to end: Double) async throws {
        let asset = AVURLAsset(url: url)
        guard let track = try await asset.loadTracks(withMediaType: .video).first else {
            throw VisionError.noVideoTrack
        }
        reader = try AVAssetReader(asset: asset)
        reader.timeRange = CMTimeRange(
            start: CMTime(seconds: start, preferredTimescale: 600),
            end: CMTime(seconds: end, preferredTimescale: 600))
        output = AVAssetReaderTrackOutput(
            track: track,
            outputSettings: [kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA])
        reader.add(output)
        guard reader.startReading() else { throw reader.error ?? VisionError.unreadable }
    }

    /// The next decoded frame, or nil at the end of the range. A decoding failure is an error,
    /// not an early end, so a damaged file can't silently truncate tracking.
    func next() throws -> (Double, CVPixelBuffer)? {
        while let sample = output.copyNextSampleBuffer() {
            if let pixels = CMSampleBufferGetImageBuffer(sample) {
                return (CMSampleBufferGetPresentationTimeStamp(sample).seconds, pixels)
            }
        }
        if reader.status == .failed {
            throw reader.error ?? VisionError.unreadable
        }
        return nil
    }
}

enum VisionError: Error {
    case noVideoTrack, unreadable
}
