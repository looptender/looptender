import Foundation

/// What the person's notification permission is, as far as the menu cares.
public enum NotificationPermission: Equatable {
    case notDetermined, denied, authorized, unknown
}

/// Whether the menu may tell the CLI it is notifying. The panel stays quiet on
/// the browser side while the menu says so, so it must be true: the menu runs,
/// it can post at all (an app bundle, not turned off), and the person has
/// allowed notifications. A menu whose permission was declined, or is still
/// being asked, is not notifying.
public enum HeartbeatPolicy {
    public static func notifying(running: Bool, canPost: Bool, permission: NotificationPermission) -> Bool {
        running && canPost && permission == .authorized
    }
}

/// Says a problem once: the first failure of a run of failures is logged, the
/// rest are not, and the next failure after a success is logged again. So a
/// heartbeat that cannot be saved is never silent and never a line every minute.
public struct SaidOnce {
    private var failing = false
    public init() {}
    /// True when this result should be logged.
    public mutating func shouldLog(failed: Bool) -> Bool {
        defer { failing = failed }
        return failed && !failing
    }
}
