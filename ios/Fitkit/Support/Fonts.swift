import SwiftUI
import UIKit

/// Newsreader, the app's display serif. The files ship in Resources under the
/// SIL Open Font License and are registered through UIAppFonts.
enum Newsreader {
    case semibold, bold

    var fontName: String {
        switch self {
        case .semibold: "Newsreader16pt16pt-SemiBold"
        case .bold: "Newsreader16pt16pt-Bold"
        }
    }

    var systemWeight: Font.Weight {
        switch self {
        case .semibold: .semibold
        case .bold: .bold
        }
    }
}

extension Font {
    /// Newsreader at a fixed size that still scales with Dynamic Type. Falls
    /// back to the system serif if the bundled font ever fails to register.
    static func newsreader(_ weight: Newsreader = .bold, size: CGFloat, relativeTo style: Font.TextStyle = .title) -> Font {
        guard UIFont(name: weight.fontName, size: size) != nil else {
            return .system(size: size, weight: weight.systemWeight, design: .serif)
        }
        return .custom(weight.fontName, size: size, relativeTo: style)
    }
}
