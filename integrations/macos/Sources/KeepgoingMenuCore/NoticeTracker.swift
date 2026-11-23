import Foundation

/// One event from `panel list --json` `notices`: the list the panel page
/// reads too, derived once in Go. The words are the CLI's; this app writes
/// none of them except the group sentence's count.
public struct PanelNotice: Decodable, Equatable {
    public let id: String
    public let kind: String
    public let severity: String
    public let loop: String
    public let name: String
    public let text: String
    public let since: String
    public let group: String
    public let groupText: String
    public var endText: String? = nil

    public init(id: String, kind: String, severity: String = "normal", loop: String, name: String, text: String, since: String = "", group: String, groupText: String, endText: String? = nil) {
        self.id = id; self.kind = kind; self.severity = severity; self.loop = loop; self.name = name
        self.text = text; self.since = since; self.group = group; self.groupText = groupText; self.endText = endText
    }
}

/// What the person is told about: a kind each can be switched on or off, with
/// defaults for the ones that are pushed.
public enum NoticeKinds {
    public static let all: [(kind: String, label: String)] = [
        ("needs_you", "A loop needs you"),
        ("limit", "A usage limit is hit"),
        ("supervisor_gone", "A supervisor stops answering"),
        ("lane_failed", "A lane fails"),
        ("hold", "A hold is set or lifted"),
        ("install", "An install is adopted or refused"),
        ("disk", "The disk is low"),
        ("bound", "A deadline or send limit is reached"),
    ]
    public static let defaultOn: Set<String> = ["needs_you", "limit", "supervisor_gone"]
}

/// Per-kind settings, kept in the app's preferences.
public final class NoticeSettings {
    private let store: UpdateStore
    public init(store: UpdateStore) { self.store = store }
    public func isOn(_ kind: String) -> Bool {
        switch store.string(forKey: "notify." + kind) {
        case "on": return true
        case "off": return false
        default: return NoticeKinds.defaultOn.contains(kind)
        }
    }
    public func set(_ kind: String, on: Bool) { store.set(on ? "on" : "off", forKey: "notify." + kind) }
    public func toggle(_ kind: String) { set(kind, on: !isOn(kind)) }
    public var map: [String: Bool] { Dictionary(uniqueKeysWithValues: NoticeKinds.all.map { ($0.kind, isOn($0.kind)) }) }
}

/// A notification to post.
public struct PostedNotice: Equatable {
    public let identifier: String
    public let body: String
    /// The loop to open when it is clicked; nil for several loops (the panel opens).
    public let loop: String?
}

/// One line of the menu's Recent section.
public struct RecentNotice: Equatable {
    public let text: String
    public let at: Date
}

/// Turns the list into notifications, once for each event: the first read is
/// a baseline and says nothing; a later read posts each id it has not seen;
/// an id that disappears from a complete read has ended. Many that begin
/// together and share a group are one notification. Everything is listed in
/// Recent, pushed or not; settings decide only what is pushed. Kept in
/// memory: a restart baselines again, so it never replays what was open.
public struct NoticeTracker {
    public static let maxRecent = 10
    private var seen: [String: PanelNotice] = [:]
    private var baselined = false
    public private(set) var recent: [RecentNotice] = []
    public init() {}

    public mutating func next(_ reading: MenuReading, now: Date, isOn: (String) -> Bool) -> [PostedNotice] {
        // A CLI that sends no list, or a read that failed, changes nothing.
        guard case .read(let list, _) = reading, let notices = list.notices else { return [] }
        let current = Dictionary(notices.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        defer {
            baselined = true
            // A cut-off read cannot say what ended, so it only adds.
            seen = list.complete ? current : seen.merging(current) { _, new in new }
        }
        guard baselined else { return [] }
        let fresh = notices.filter { seen[$0.id] == nil }
        if list.complete {
            for (id, old) in seen where current[id] == nil {
                if let end = old.endText, !end.isEmpty { add(Contract.text(end), now) }
            }
        }
        for n in fresh { add(Contract.text(n.text), Contract.date(n.since).map { min($0, now) } ?? now) }
        var posted: [PostedNotice] = []
        var groups: [String: [PanelNotice]] = [:]
        var order: [String] = []
        for n in fresh where isOn(n.kind) {
            if groups[n.group] == nil { order.append(n.group) }
            groups[n.group, default: []].append(n)
        }
        for g in order {
            let members = groups[g]!
            if members.count == 1 {
                posted.append(PostedNotice(identifier: "keepgoing.notice." + members[0].id, body: Contract.text(members[0].text), loop: members[0].loop))
            } else {
                let sentence = Contract.text(members[0].groupText).replacingOccurrences(of: "{n}", with: String(members.count))
                posted.append(PostedNotice(identifier: "keepgoing.notice.group." + members.map(\.id).joined(separator: "+"), body: sentence, loop: nil))
            }
        }
        return posted
    }

    private mutating func add(_ text: String, _ at: Date) {
        recent.insert(RecentNotice(text: text, at: at), at: 0)
        if recent.count > Self.maxRecent { recent.removeLast(recent.count - Self.maxRecent) }
    }
}
