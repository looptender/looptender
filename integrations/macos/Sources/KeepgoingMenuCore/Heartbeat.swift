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
