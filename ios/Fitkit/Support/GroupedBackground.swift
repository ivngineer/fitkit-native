import SwiftUI
import UIKit

extension Color {
    /// Grouped list colors as on a full screen, even inside a sheet, where
    /// dark mode would otherwise lift them to grey.
    static let screenGroupedBackground = Color(uiColor: .base(.systemGroupedBackground))
    static let screenGroupedRow = Color(uiColor: .base(.secondarySystemGroupedBackground))
}

private extension UIColor {
    static func base(_ color: UIColor) -> UIColor {
        UIColor { traits in
            color.resolvedColor(with: traits.modifyingTraits { $0.userInterfaceLevel = .base })
        }
    }
}
