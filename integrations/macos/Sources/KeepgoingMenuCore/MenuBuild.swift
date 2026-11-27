import Foundation

/// Packaging metadata captured once at startup, never re-read through `current`.
/// A deployment folder label is not a substitute for source provenance.
public struct MenuBuildStamp: Equatable {
    public let version: String
    public let revision: String?
    public let modified: Bool

    public init(version: String?, revision: String?, modified: Bool = false) {
        self.version = version.flatMap { value in
            value.count <= 32 && !value.isEmpty && value.allSatisfy { "0123456789.".contains($0) } ? value : nil
        } ?? "unknown"
        self.revision = revision.flatMap { value in
            [40, 64].contains(value.count) && value.allSatisfy { "0123456789abcdef".contains($0) } ? value : nil
        }
        self.modified = modified
    }

    public static func capture(_ info: [String: Any]) -> MenuBuildStamp {
        MenuBuildStamp(version: info["CFBundleShortVersionString"] as? String,
                       revision: info["LooptenderSourceRevision"] as? String,
                       modified: info["LooptenderSourceModified"] as? Bool ?? false)
    }

    public var text: String {
        "Menu " + version + " · " + (revision.map { "build " + $0.prefix(8) } ?? "build unknown") + (modified ? " · modified source" : "")
    }
}

extension MenuBuilder {
    static func identityRows(_ stamp: MenuBuildStamp, reading: MenuReading, update: UpdateOffer?) -> [MenuRow] {
        func row(_ text: String, id: String) -> MenuRow {
            MenuRow(kind: .status, title: text, detail: "", symbol: "", accessibilityLabel: text,
                    accessibilityHelp: "Running menu metadata is captured at startup. CLI identity is from the last successful saved read.",
                    toolTip: text, command: nil, semanticID: id)
        }
        var rows = [row(stamp.text, id: "menu-build")]
        if case .read(let list, _) = reading, let cli = list.installedBuild.map(Contract.text), !cli.isEmpty {
            rows.append(row("CLI " + shortened(cli, to: 60) + " · last read", id: "cli-build"))
        } else {
            rows.append(row("CLI build unavailable", id: "cli-build"))
        }
        if let installed = update?.availableLabel {
            rows.append(row("Installed menu differs · " + shortened(Contract.text(installed), to: 40), id: "menu-build-mismatch"))
        }
        return rows
    }
}
