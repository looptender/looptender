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
    private var rule = OverdueNotices()
    private let center: UNUserNotificationCenter?

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
        center?.requestAuthorization(options: [.alert]) { _, _ in }
    }

    /// The gaps already told, for the menu that replaces this one.
    func exportTold() -> String { rule.exportTold() }
    func restoreTold(_ text: String) { rule.restoreTold(text) }

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
        if response.notification.request.content.userInfo["relaunch"] as? Bool == true {
            DispatchQueue.main.async { [weak self] in self?.onUpdateClick?() }
        }
        completionHandler()
    }

    /// Called on the main queue after each read.
    func update(_ reading: MenuReading) {
        guard let center else { return }
        for notice in rule.next(reading) {
            let content = UNMutableNotificationContent()
            content.title = notice.title
            content.body = notice.body
            center.add(UNNotificationRequest(identifier: notice.identifier, content: content, trigger: nil))
        }
    }
}
