import XCTest

/// End-to-end smoke test against a locally running server:
///
///     cd server && FITKIT_DEMO_ANALYSIS=1 FITKIT_IMPORT_PIN_LIMIT=24 go run ./cmd/fitkitd
///
/// Set FITKIT_UITEST_PINTEREST to choose which public profile to import, and
/// FITKIT_UITEST_SERVER to point the app at a server other than
/// http://localhost:8080 (handy for keeping a demo server apart from one
/// running on live provider keys). xcodebuild only forwards variables with a
/// TEST_RUNNER_ prefix.
final class FitkitUITests: XCTestCase {
    private var baseURL: String {
        ProcessInfo.processInfo.environment["FITKIT_UITEST_SERVER"] ?? "http://localhost:8080"
    }

    private var serverURL: URL { URL(string: baseURL + "/healthz")! }

    override func setUp() async throws {
        continueAfterFailure = false
        do {
            let (_, response) = try await URLSession.shared.data(from: serverURL)
            guard (response as? HTTPURLResponse)?.statusCode == 200 else { throw URLError(.badServerResponse) }
        } catch {
            throw XCTSkip("Fitkit server isn't running at \(serverURL)")
        }
    }

    @MainActor
    func testSignUpOnboardImportAndShopAPin() throws {
        let app = XCUIApplication()
        app.launchArguments = ["-resetSession", "-uiTesting", "-serverBaseURL", baseURL]
        app.launch()

        // Sign up.
        let email = app.textFields["auth.email"]
        XCTAssertTrue(email.waitForExistence(timeout: 10))
        snapshot(app, "1-auth")
        email.tap()
        email.typeText("uitest-\(UUID().uuidString.prefix(8).lowercased())@example.com")
        let password = app.secureTextFields["auth.password"]
        password.tap()
        password.typeText("correct-horse-battery")
        app.buttons["auth.submit"].tap()
        // The prompt lands on the first onboarding step and swallows taps.
        dismissSavePasswordPrompt(app)

        // Onboarding step 1: referral source saves on tap.
        let referral = app.buttons["referral.friend"]
        XCTAssertTrue(referral.waitForExistence(timeout: 10))
        snapshot(app, "2-onboarding-referral")
        referral.tap()

        // Onboarding step 2: Pinterest handle.
        let handle = app.textFields["pinterest.handle"]
        XCTAssertTrue(handle.waitForExistence(timeout: 10))
        let profile = ProcessInfo.processInfo.environment["FITKIT_UITEST_PINTEREST"] ?? "pinterest"
        handle.tap()
        handle.typeText("https://www.pinterest.com/\(profile)/")
        snapshot(app, "3-onboarding-pinterest")
        app.buttons["pinterest.save"].tap()

        // Home: pins arrive as the background import progresses.
        let firstPin = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'pin.'")).firstMatch
        XCTAssertTrue(firstPin.waitForExistence(timeout: 90), "No pins were imported")
        sleep(4) // let thumbnails load for the screenshot
        dismissSavePasswordPrompt(app)
        snapshot(app, "4-home")

        // Pin detail: demo analysis breaks the look into items.
        firstPin.tap()
        let opened = app.buttons["Done"].waitForExistence(timeout: 10)
        if !opened {
            snapshot(app, "4b-after-tap")
            snapshot(XCUIApplication(bundleIdentifier: "com.apple.springboard"), "4c-springboard")
        }
        XCTAssertTrue(opened, "Pin sheet didn't open")
        let found = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS 'items found' OR label CONTAINS '1 item found'")).firstMatch
        XCTAssertTrue(found.waitForExistence(timeout: 60), "Analysis never finished")
        sleep(1)
        snapshot(app, "5-pin-detail")

        app.swipeUp()
        snapshot(app, "6-pin-detail-expanded")
        app.buttons["Done"].tap()

        // Quick actions put a pin in the cart, and the cart sheet lists it.
        firstPin.press(forDuration: 1.2)
        let addToCart = app.buttons["Add to Cart"]
        XCTAssertTrue(addToCart.waitForExistence(timeout: 5), "Quick actions should offer the cart")
        addToCart.tap()
        app.buttons["home.cart"].tap()
        XCTAssertTrue(app.navigationBars["Cart"].waitForExistence(timeout: 5), "Cart sheet didn't open")
        XCTAssertTrue(app.staticTexts["Cart Is Empty"].waitForNonExistence(timeout: 5), "Cart should hold the pin")
        snapshot(app, "7-cart")
        app.buttons["Done"].tap()

        // Removing a pin takes it out of the grid for good.
        let removedIdentifier = firstPin.identifier
        firstPin.press(forDuration: 1.2)
        let remove = app.buttons["Remove from Fitkit"]
        XCTAssertTrue(remove.waitForExistence(timeout: 5), "Quick actions should offer removal")
        remove.tap()
        XCTAssertTrue(app.buttons[removedIdentifier].waitForNonExistence(timeout: 10), "Removed pin stayed in the grid")
        // The first removals explain that the pin is still on Pinterest.
        let notice = app.descendants(matching: .any).matching(identifier: "home.removalNotice").firstMatch
        XCTAssertTrue(notice.waitForExistence(timeout: 5), "Removal notice should appear")
        snapshot(app, "8-after-remove")

        // Account screen.
        app.buttons["home.account"].tap()
        let username = app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@ OR value CONTAINS %@", "@\(profile)", "@\(profile)")).firstMatch
        XCTAssertTrue(username.waitForExistence(timeout: 5), "Account screen should show the Pinterest username")
        snapshot(app, "9-account")
    }

    /// iOS offers to save the new account's password in a system prompt.
    @MainActor
    private func dismissSavePasswordPrompt(_ app: XCUIApplication) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for candidate in [app.buttons["Not Now"], springboard.buttons["Not Now"]] where candidate.waitForExistence(timeout: 2) {
            candidate.tap()
            // Let the system prompt finish animating away before interacting.
            _ = candidate.waitForNonExistence(timeout: 5)
            sleep(1)
            return
        }
    }

    @MainActor
    private func snapshot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
    }
}
