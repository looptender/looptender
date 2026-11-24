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

/// The notification settings the CLI keeps in one place, shared by the menu,
/// the panel and push: which kinds are on, whether the panel's person chose
/// browser notifications, and whether the menu is notifying now.
public struct PanelNotify: Decodable, Equatable {
    public var kinds: [String: Bool] = [:]
    public var browser: Bool = false
    public var menu: PanelNotifyMenu = PanelNotifyMenu()
    public init(kinds: [String: Bool] = [:], browser: Bool = false, menu: PanelNotifyMenu = PanelNotifyMenu()) { self.kinds = kinds; self.browser = browser; self.menu = menu }
}
public struct PanelNotifyMenu: Decodable, Equatable {
    public var notifying: Bool = false
    /// When the menu last said it was running: what a menu starting up reads to
    /// find what began while it was down.
    public var lastRunningAt: String? = nil
    public init(notifying: Bool = false, lastRunningAt: String? = nil) { self.notifying = notifying; self.lastRunningAt = lastRunningAt }
}

/// Whether a kind is pushed: the CLI's shared setting, else the default (a CLI
/// that predates the setting).
public enum NoticeSettings {
    public static func isOn(_ kind: String, _ notify: PanelNotify?) -> Bool {
        notify?.kinds[kind] ?? NoticeKinds.defaultOn.contains(kind)
    }
    public static func map(_ notify: PanelNotify?) -> [String: Bool] {
        Dictionary(uniqueKeysWithValues: NoticeKinds.all.map { ($0.kind, isOn($0.kind, notify)) })
    }
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
        var catchUp: [PanelNotice] = []
        // A CLI that sends no list, or a read that failed, changes nothing.
        guard case .read(let list, _) = reading, let notices = list.notices else { return [] }
        let current = Dictionary(notices.map { ($0.id, $0) }, uniquingKeysWith: { a, _ in a })
        defer {
            baselined = true
            // A cut-off read cannot say what ended, so it only adds.
            seen = list.complete ? current : seen.merging(current) { _, new in new }
        }
        // Started after being down: an urgent question that began since the
        // menu last ran is still news. Only that, only on the first read, so it
        // is never a storm.
        var fresh = notices.filter { seen[$0.id] == nil }
        if !baselined {
            guard let last = list.notify?.menu.lastRunningAt.flatMap(Contract.date) else { return [] }
            catchUp = notices.filter { $0.kind == "needs_you" && $0.severity == "urgent" && (Contract.date($0.since).map { $0 > last && $0 <= now } ?? false) }
            fresh = catchUp
            for n in catchUp { add(Contract.text(n.text), Contract.date(n.since).map { min($0, now) } ?? now) }
        } else {
            if list.complete {
                for (id, old) in seen where current[id] == nil {
                    if let end = old.endText, !end.isEmpty { add(Contract.text(end), now) }
                }
            }
            for n in fresh { add(Contract.text(n.text), Contract.date(n.since).map { min($0, now) } ?? now) }
        }
        var posted: [PostedNotice] = []
        var groups: [String: [PanelNotice]] = [:]
        var order: [String] = []
        for n in fresh where isOn(n.kind) {
            if groups[n.group] == nil { order.append(n.group) }
            groups[n.group, default: []].append(n)
        }
        // A group's count is its real total in this list, not only the members
        // that are new (a kind that is off never forms a group with one that is on).
        var total: [String: Int] = [:]
        // (After being down, only what began since the menu last ran is told, so only that is counted.)
        for n in (catchUp.isEmpty && baselined ? notices : fresh) { total[n.group, default: 0] += 1 }
        for g in order {
            let members = groups[g]!
            if members.count == 1 && (total[g] ?? 1) == 1 {
                posted.append(PostedNotice(identifier: "keepgoing.notice." + members[0].id, body: Contract.text(members[0].text), loop: members[0].loop))
            } else {
                let sentence = Contract.text(members[0].groupText).replacingOccurrences(of: "{n}", with: String(max(members.count, total[g] ?? 0)))
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
