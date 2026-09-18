import SwiftUI

struct PinCell: View {
    let pin: Pin
    let imageURL: URL?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            PinImage(url: imageURL, dominantColor: pin.dominantColor)
                .aspectRatio(max(pin.aspectRatio, 0.3), contentMode: .fit)
                .clipShape(.rect(cornerRadius: 16))

            Text(pin.title)
                .font(.caption.weight(.medium))
                .foregroundStyle(.primary)
                .lineLimit(1)
                .padding(.horizontal, 4)
        }
        .contentShape(.rect)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(pin.title)
        .accessibilityHint("Shows where to buy the items in this pin")
        .accessibilityAddTraits(.isButton)
    }
}

/// Remote image with the pin's dominant color as a placeholder. Images are
/// kept on the device, so ones seen before show up offline too.
struct PinImage: View {
    let url: URL?
    var dominantColor: String = ""
    /// Symbol shown when there is no image URL at all.
    var emptySymbol: String?

    var body: some View {
        let placeholder = Color(hex: dominantColor)?.opacity(0.35) ?? Color(.secondarySystemFill)
        Rectangle()
            .fill(placeholder)
            .overlay {
                if url == nil, let emptySymbol {
                    Image(systemName: emptySymbol)
                        .foregroundStyle(.secondary)
                }
            }
            .overlay { StoredImage(url: url) }
            .clipped()
    }
}

private struct StoredImage: View {
    let url: URL?
    @State private var image: UIImage?
    @State private var failed = false

    var body: some View {
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFill()
            } else if failed {
                Image(systemName: "photo")
                    .foregroundStyle(.secondary)
            } else {
                Color.clear
            }
        }
        .task(id: url) { await load() }
    }

    private func load() async {
        image = nil
        failed = false
        guard let url else { return }
        do {
            let loaded = try await ImageCache.shared.image(for: url)
            withAnimation(.easeOut(duration: 0.2)) { image = loaded }
        } catch is CancellationError {
        } catch {
            failed = !Task.isCancelled
        }
    }
}
