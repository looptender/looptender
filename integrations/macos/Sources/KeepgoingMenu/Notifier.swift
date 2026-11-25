import Foundation
import KeepgoingMenuCore
import UserNotifications

/// Posts the notifications OverdueNotices decides on, from the reads the
/// menu already makes (when it starts, when it is opened, and on its timer).
///
/// macOS posts notifications only for an app bundle; asking from a bare
/// executable ends the process. So nothing here touches the notification
/// API unless the menu runs from a bundle (scripts/make-app.sh builds one).
/// Without one, or with --no-notifications, the menu works as before.
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    /// Called on the main queue when the person clicks the update notification.
    var onUpdateClick: (() -> Void)?
    /// Called on the main queue with the loop key of a clicked event, or "" for several.
    var onNoticeClick: ((String) -> Void)?
    private var rule = OverdueNotices()
    private let center: UNUserNotificationCenter?
    /// Whether this menu posts notifications at all (an app bundle, and not turned off).
    var canPost: Bool { center != nil }
    /// What the person has allowed, read from the system; unknown until first read.
    private(set) var permission: NotificationPermission = .unknown
    /// Called on the main queue when the permission changes (granted, or turned off in System Settings).
    var onPermissionChange: (() -> Void)?

    /// Reads the system's answer now and, when it differs from the last, says so.
    func refreshPermission() {
        guard let center else { return }
        center.getNotificationSettings { [weak self] settings in
            let now: NotificationPermission
            switch settings.authorizationStatus {
            case .authorized, .provisional: now = .authorized
            case .denied: now = .denied
            case .notDetermined: now = .notDetermined
            default: now = .unknown
            }
            DispatchQueue.main.async {
                guard let self, now != self.permission else { return }
                self.permission = now
                self.onPermissionChange?()
            }
        }
    }

    init(enabled: Bool) {
        guard enabled else {
            center = nil
            super.init()
            return
        }
        guard Bundle.main.bundleIdentifier != nil else {
            center = nil
            super.init()
            fputs("KeepgoingMenu: not running from an app bundle, so it cannot post notifications; build one with integrations/macos/scripts/make-app.sh\n", stderr)
            return
        }
        center = UNUserNotificationCenter.current()
        super.init()
        center?.delegate = self
        // macOS asks the person once; if they decline, nothing is posted.
        center?.requestAuthorization(options: [.alert]) { [weak self] _, _ in self?.refreshPermission() }
        refreshPermission()
    }

    /// The gaps already told, for the menu that replaces this one.
    func exportTold() -> String { rule.exportTold() }
    func restoreTold(_ text: String) { rule.restoreTold(text) }

    /// One event from the list. Without permission nothing shows and nothing
    /// is asked again.
    func post(notice: PostedNotice) {
        guard let center else { return }
        let content = UNMutableNotificationContent()
        content.title = "looptender"
        content.body = notice.body
        content.userInfo = ["loop": notice.loop ?? ""]
        center.add(UNNotificationRequest(identifier: notice.identifier, content: content, trigger: nil))
    }

    static let updateIdentifier = "menu-update-available"

    /// One notification about the menu's own update. The caller says when
    /// (once for each installed build); without permission nothing shows and
    /// nothing is asked again.
    func post(update text: String, clickable: Bool) {
        guard let center else { return }
        let content = UNMutableNotificationContent()
        content.title = text
        content.userInfo = clickable ? ["relaunch": true] : [:]
        center.add(UNNotificationRequest(identifier: clickable ? Self.updateIdentifier : "menu-updated", content: content, trigger: nil))
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse, withCompletionHandler completionHandler: @escaping () -> Void) {
        let info = response.notification.request.content.userInfo
        if info["relaunch"] as? Bool == true {
            DispatchQueue.main.async { [weak self] in self?.onUpdateClick?() }
        } else if let loop = info["loop"] as? String {
            DispatchQueue.main.async { [weak self] in self?.onNoticeClick?(loop) }
        }
        completionHandler()
    }

    /// Called on the main queue after each read.
    func update(_ reading: MenuReading) {
        guard let center else { return }
        // The list the panel reads carries an overdue check as a notice too:
        // with it, the older rule would post it a second time.
        if case .read(let list, _) = reading, list.notices != nil { return }
        for notice in rule.next(reading) {
            let content = UNMutableNotificationContent()
            content.title = notice.title
            content.body = notice.body
            center.add(UNNotificationRequest(identifier: notice.identifier, content: content, trigger: nil))
        }
    }
}
