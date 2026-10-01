import Foundation
import Vision

/// Writes one compact JSON object per line to stdout.
final class JSONLines {
    private let encoder = JSONEncoder()

    func write<T: Encodable>(_ value: T) throws {
        var data = try encoder.encode(value)
        data.append(0x0A)
        FileHandle.standardOutput.write(data)
    }
}

/// Converts a Vision rectangle (bottom-left origin) to [x, y, w, h] with a top-left origin.
func box(_ r: CGRect) -> [Double] {
    [r.minX, 1 - r.maxY, r.width, r.height].map(round4)
}

/// Converts a Vision point (bottom-left origin) to [x, y] with a top-left origin.
func point(_ p: CGPoint) -> [Double] {
    [p.x, 1 - p.y].map(round4)
}

private func round4(_ v: CGFloat) -> Double {
    (Double(v) * 10_000).rounded() / 10_000
}
