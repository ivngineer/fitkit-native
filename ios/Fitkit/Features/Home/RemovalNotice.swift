import SwiftUI

/// A one-line explainer shown the first couple of times someone removes a pin,
/// so nobody worries Fitkit touched their Pinterest account. It sits above the
/// grid, dismisses itself, and never blocks what's underneath.
struct RemovalNotice: View {
    var onDismiss: () -> Void

    /// How long the notice stays up when it isn't dismissed by hand.
    static let duration: Duration = .seconds(6)

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: "eye.slash")
                .foregroundStyle(.secondary)
            Text("Removed pins stay in your Pinterest account — they just don't show up here.")
                .font(.footnote)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
            Button("Dismiss", systemImage: "xmark", action: onDismiss)
                .labelStyle(.iconOnly)
                .font(.footnote.weight(.semibold))
                .foregroundStyle(.secondary)
                .buttonStyle(.plain)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
        .background(.regularMaterial, in: .rect(cornerRadius: 16))
        .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(.separator))
        .shadow(color: .black.opacity(0.12), radius: 12, y: 4)
        .padding(.horizontal, 16)
        .padding(.bottom, 12)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("home.removalNotice")
        .task {
            try? await Task.sleep(for: Self.duration)
            onDismiss()
        }
    }
}
