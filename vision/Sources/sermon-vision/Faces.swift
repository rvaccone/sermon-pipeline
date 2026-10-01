import AVFoundation
import Vision

/// One sampled frame for thumbnail selection.
struct FaceRecord: Encodable {
    struct Face: Encodable {
        let box: [Double]
        let quality: Double  // Vision's capture quality: sharp, well lit, frontal, eyes open
    }
    let t: Double
    let faces: [Face]
    let text: [[Double]]  // boxes around on-screen text, e.g. slides behind the preacher
}

/// Samples `rate` frames per second (default one every two seconds) at exact times.
func analyzeFaces(_ args: Arguments) async throws {
    let rate = args.rate ?? 0.5
    let generator = AVAssetImageGenerator(asset: AVURLAsset(url: args.video))
    generator.requestedTimeToleranceBefore = .zero
    generator.requestedTimeToleranceAfter = .zero
    generator.appliesPreferredTrackTransform = true

    let output = JSONLines()
    var time = args.start
    while time < args.end {
        let image = try await generator.image(at: CMTime(seconds: time, preferredTimescale: 600)).image
        let quality = VNDetectFaceCaptureQualityRequest()
        let text = VNDetectTextRectanglesRequest()
        try VNImageRequestHandler(cgImage: image).perform([quality, text])
        try output.write(FaceRecord(
            t: time,
            faces: (quality.results ?? []).map {
                FaceRecord.Face(box: box($0.boundingBox), quality: Double($0.faceCaptureQuality ?? 0))
            },
            text: (text.results ?? []).map { box($0.boundingBox) }
        ))
        time += 1 / rate
    }
}
