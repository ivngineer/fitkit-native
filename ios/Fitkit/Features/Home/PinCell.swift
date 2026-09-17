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

/// Remote image with the pin's dominant color as a placeholder.
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
            .overlay {
                AsyncImage(url: url, transaction: Transaction(animation: .easeOut(duration: 0.2))) { phase in
                    switch phase {
                    case .success(let image):
                        image.resizable().scaledToFill()
                    case .failure:
                        Image(systemName: "photo")
                            .foregroundStyle(.secondary)
                    default:
                        Color.clear
                    }
                }
            }
            .clipped()
    }
}
