import SwiftUI

extension View {
    /// Keeps a form or list at a comfortable reading width on wide screens
    /// by insetting its content, so the background and scrolling still span
    /// the whole window. Phones are narrower than the limit and don't change.
    func readableContentWidth(_ maxWidth: CGFloat = 600) -> some View {
        modifier(ReadableContentWidth(maxWidth: maxWidth))
    }
}

private struct ReadableContentWidth: ViewModifier {
    let maxWidth: CGFloat
    @State private var width: CGFloat = 0

    func body(content: Content) -> some View {
        Group {
            // Only set margins when there's room to spare: an explicit zero
            // margin stretches a grouped form edge to edge, with square rows
            // and full-width separators.
            if width > maxWidth {
                content.contentMargins(.horizontal, (width - maxWidth) / 2, for: .scrollContent)
            } else {
                content
            }
        }
        .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { width = $0 }
    }
}
