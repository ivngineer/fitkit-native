import SafariServices
import UIKit

/// Shows a page in Safari over whatever is on screen, and says when the user
/// closes it. Store checkouts open from sheets stacked on sheets, where a
/// SwiftUI cover per screen gets fiddly; this presents straight from the top
/// view controller instead.
@MainActor
enum SafariPresenter {
    private static var delegates: [ObjectIdentifier: Delegate] = [:]

    /// `onFinish` runs once the user has closed Safari and it's off screen,
    /// so it can present an alert right away.
    static func open(_ url: URL, onFinish: @escaping () -> Void = {}) {
        guard let top = topViewController() else { return }
        let safari = SFSafariViewController(url: url)
        let delegate = Delegate { controller in
            delegates[ObjectIdentifier(controller)] = nil
            whenGone(controller, then: onFinish)
        }
        delegates[ObjectIdentifier(safari)] = delegate
        safari.delegate = delegate
        safari.modalPresentationStyle = .fullScreen
        top.present(safari, animated: true)
    }

    private static func whenGone(_ controller: UIViewController, then action: @escaping () -> Void) {
        if let coordinator = controller.transitionCoordinator {
            coordinator.animate(alongsideTransition: nil) { _ in action() }
            return
        }
        Task {
            for _ in 0..<40 where controller.presentingViewController != nil {
                try? await Task.sleep(for: .milliseconds(50))
            }
            action()
        }
    }

    private static func topViewController() -> UIViewController? {
        let scene = UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .first { $0.activationState == .foregroundActive } ?? UIApplication.shared.connectedScenes.first as? UIWindowScene
        var top = scene?.keyWindow?.rootViewController
        while let presented = top?.presentedViewController, !presented.isBeingDismissed {
            top = presented
        }
        return top
    }

    private final class Delegate: NSObject, SFSafariViewControllerDelegate {
        let finish: (SFSafariViewController) -> Void

        init(finish: @escaping (SFSafariViewController) -> Void) {
            self.finish = finish
        }

        func safariViewControllerDidFinish(_ controller: SFSafariViewController) {
            finish(controller)
        }
    }
}
