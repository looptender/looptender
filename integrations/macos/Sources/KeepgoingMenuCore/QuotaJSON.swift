import Foundation

/// Inspect raw object keys before JSONDecoder discards duplicate occurrences.
/// Only quota provenance and its enclosing path are guarded; legacy unrelated
/// fields keep their existing decoding behavior. JSONDecoder still validates
/// values and the complete schema after this bounded structural pass.
enum QuotaJSON {
    static func validateProvenanceKeys(_ data: Data) throws {
        var scan = Scanner(bytes: Array(data))
        if scan.bytes.starts(with: [0xEF, 0xBB, 0xBF]) { scan.offset = 3 }
        try scan.value(.root, depth: 0)
        scan.whitespace()
        guard scan.offset == scan.bytes.count else { throw ClientFailure.invalidResponse }
    }
    private enum Context {
        case root, quotaList, accounts, account, summary, provenance, other
        func child(_ key: String) -> Context {
            switch (self, key) {
            case (.root, "accountQuotas"): return .quotaList
            case (.quotaList, "accounts"): return .accounts
            case (.account, "summary"): return .summary
            case (.summary, "provenance"): return .provenance
            default: return .other
            }
        }
        func guarded(_ key: String) -> Bool {
            switch self {
            case .root: return key == "accountQuotas"
            case .quotaList: return key == "accounts"
            case .account: return key == "summary"
            case .summary: return key == "provenance"
            case .provenance: return true
            default: return false
            }
        }
    }
    private struct Scanner {
        let bytes: [UInt8]
        var offset = 0
        mutating func whitespace() {
            while offset < bytes.count && [9, 10, 13, 32].contains(bytes[offset]) { offset += 1 }
        }
        mutating func take(_ byte: UInt8) throws {
            whitespace()
            guard offset < bytes.count, bytes[offset] == byte else { throw ClientFailure.invalidResponse }
            offset += 1
        }
        mutating func string() throws -> Range<Int> {
            try take(34)
            let start = offset - 1
            while offset < bytes.count {
                let byte = bytes[offset]
                offset += 1
                if byte == 34 { return start..<offset }
                if byte == 92 {
                    guard offset < bytes.count else { throw ClientFailure.invalidResponse }
                    offset += 1
                }
            }
            throw ClientFailure.invalidResponse
        }
        mutating func value(_ context: Context, depth: Int) throws {
            guard depth <= 512 else { throw ClientFailure.invalidResponse }
            whitespace()
            guard offset < bytes.count else { throw ClientFailure.invalidResponse }
            switch bytes[offset] {
            case 123: // object
                offset += 1
                whitespace()
                if offset < bytes.count, bytes[offset] == 125 { offset += 1; return }
                var seen = Set<String>()
                while true {
                    let range = try string()
                    // Decode the individual string, never an object/dictionary:
                    // escaped-equivalent spellings must count as the same key.
                    let key = context == .other ? "" : try JSONDecoder().decode(String.self, from: Data(bytes[range]))
                    if context.guarded(key), !seen.insert(key).inserted { throw ClientFailure.invalidResponse }
                    try take(58)
                    try value(context.child(key), depth: depth + 1)
                    whitespace()
                    guard offset < bytes.count else { throw ClientFailure.invalidResponse }
                    if bytes[offset] == 125 { offset += 1; return }
                    try take(44)
                }
            case 91: // array
                offset += 1
                whitespace()
                if offset < bytes.count, bytes[offset] == 93 { offset += 1; return }
                while true {
                    try value(context == .accounts ? .account : .other, depth: depth + 1)
                    whitespace()
                    guard offset < bytes.count else { throw ClientFailure.invalidResponse }
                    if bytes[offset] == 93 { offset += 1; return }
                    try take(44)
                }
            case 34:
                _ = try string()
            default:
                let start = offset
                while offset < bytes.count && ![9, 10, 13, 32, 44, 93, 125].contains(bytes[offset]) { offset += 1 }
                guard offset > start else { throw ClientFailure.invalidResponse }
            }
        }
    }
}
