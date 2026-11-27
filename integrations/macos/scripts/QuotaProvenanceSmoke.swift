import Foundation

@main struct QuotaProvenanceSmoke {
    static func main() throws {
        if CommandLine.arguments.count == 2 {
            try QuotaProvenanceFixture.run(Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1])))
        } else {
            try QuotaProvenanceFixture.run()
        }
        print("Native provenance PASS: pinned producer, three legacy sources, strict v1 absence/null/malformed matrix, clocks/reset/failure and unchanged authority")
    }
}
