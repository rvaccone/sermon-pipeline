import Foundation

struct UsageError: Error {}

/// Command-line arguments: a command, a video, a time range and a sampling rate.
struct Arguments {
    let command: String
    let video: URL
    let start: Double
    let end: Double
    let rate: Double?

    init<S: Sequence>(_ raw: S) throws where S.Element == String {
        var positional: [String] = []
        var options: [String: Double] = [:]
        var iterator = raw.makeIterator()
        while let arg = iterator.next() {
            if arg.hasPrefix("--") {
                guard let value = iterator.next().flatMap(Double.init) else { throw UsageError() }
                options[String(arg.dropFirst(2))] = value
            } else {
                positional.append(arg)
            }
        }
        guard positional.count == 2, let start = options["start"], let end = options["end"], end > start else {
            throw UsageError()
        }
        command = positional[0]
        video = URL(fileURLWithPath: positional[1])
        self.start = start
        self.end = end
        rate = options["rate"]
    }
}
