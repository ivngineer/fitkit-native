import Foundation
import Observation

/// Picks the pieces and sizes for "Get this look", then either places a Fitkit
/// order ("Buy": Fitkit's buyers purchase the pieces) or asks the server for a
/// checkout plan to pay each store directly. The server checks everything
/// again; this only keeps the user from sending something obviously incomplete.
@Observable
final class LookBuilderModel {
    struct Piece: Identifiable {
        let item: DetectedItem
        var included: Bool
        var listingURL: String
        var variantID: Int64?
        /// Free-text size for stores whose sizes Fitkit can't read.
        var size: String

        var id: String { item.id }
        var listing: Listing? { item.listings.first { $0.url == listingURL } }
    }

    enum Phase: Equatable {
        case loading
        case ready
    }

    let pin: Pin
    private(set) var phase: Phase = .loading
    var pieces: [Piece]
    private(set) var options: [String: ListingOptions] = [:]
    private(set) var addresses: [Address] = []
    var addressID: String?
    private(set) var rememberedSizes: [String: String] = [:]
    private(set) var isSubmitting = false
    var errorMessage: String?
    /// Name or nickname sent with Fitkit orders; remembered on this device.
    var buyerName: String = UserDefaults.standard.string(forKey: LookBuilderModel.buyerNameKey) ?? ""
    /// One key per order attempt, so a retried tap returns the same order
    /// instead of placing a second one.
    private var orderKey = UUID().uuidString

    static let maxOptionURLs = 30
    static let buyerNameKey = "orderBuyerName"
    /// The server's limits for an order's name and a piece's size.
    static let maxBuyerNameLength = 60
    static let maxSizeLength = 16

    init(pin: Pin, result: AnalysisResult) {
        self.pin = pin
        pieces = result.items.compactMap { item in
            guard let first = item.listings.first else { return nil }
            return Piece(item: item, included: first.inStock != false, listingURL: first.url, size: "")
        }
    }

    var selectedAddress: Address? { addresses.first { $0.id == addressID } }

    func product(for piece: Piece) -> ListingOptions.Product? {
        options[piece.listingURL]?.product
    }

    func method(for piece: Piece) -> CheckoutMethod? {
        options[piece.listingURL]?.method
    }

    /// The piece needs a size picked in the app before checkout.
    func needsVariant(_ piece: Piece) -> Bool {
        guard let product = product(for: piece) else { return false }
        return product.variants.count > 1 && piece.variantID == nil
    }

    var includedCount: Int { pieces.filter(\.included).count }

    var canSubmit: Bool {
        !isSubmitting && includedCount > 0 && !pieces.contains { $0.included && needsVariant($0) }
    }

    var trimmedBuyerName: String { buyerName.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// "Buy" also needs a name the buyers can address the order to.
    var canBuy: Bool {
        canSubmit && !trimmedBuyerName.isEmpty && trimmedBuyerName.count <= Self.maxBuyerNameLength
    }

    // MARK: Loading

    func load(api: APIClient, app: AppModel) async {
        let urls = Array(pieces.map(\.listingURL).prefix(Self.maxOptionURLs))
        async let addressList = api.addresses()
        async let sizeProfile = api.sizes()
        async let optionList = api.listingOptions(pinID: pin.id, urls: urls)
        do {
            addresses = try await addressList
            addressID = addressID ?? (addresses.first { $0.isDefault } ?? addresses.first)?.id
            rememberedSizes = (try? await sizeProfile) ?? [:]
            // Without options every piece still checks out on its store page.
            if let list = try? await optionList {
                options = Dictionary(list.map { ($0.url, $0) }, uniquingKeysWith: { first, _ in first })
            }
            for index in pieces.indices { applyRememberedSize(at: index) }
        } catch is CancellationError {
            return
        } catch {
            if app.handleUnauthorized(error) { return }
            errorMessage = error.localizedDescription
        }
        phase = .ready
    }

    /// Called after the address book changes, e.g. a new address was added.
    func reloadAddresses(api: APIClient, selecting id: String? = nil) async {
        guard let list = try? await api.addresses() else { return }
        addresses = list
        if let id { addressID = id } else if selectedAddress == nil { addressID = list.first { $0.isDefault }?.id }
    }

    // MARK: Editing

    /// Fills in the size the user picked last time for this kind of piece.
    private func applyRememberedSize(at index: Int) {
        let piece = pieces[index]
        let remembered = rememberedSizes[piece.item.category] ?? ""
        if piece.size.isEmpty { pieces[index].size = remembered }
        guard piece.variantID == nil, let product = product(for: piece) else { return }
        if product.variants.count == 1 {
            pieces[index].variantID = product.variants[0].id
        } else if let match = Self.variant(in: product, matching: remembered) {
            pieces[index].variantID = match.id
        }
    }

    /// The in-stock variant whose options include the size.
    static func variant(in product: ListingOptions.Product, matching size: String) -> ListingOptions.Product.Variant? {
        let size = size.trimmingCharacters(in: .whitespaces)
        guard !size.isEmpty else { return nil }
        return product.variants.first { variant in
            variant.available && (variant.options ?? []).contains { $0.caseInsensitiveCompare(size) == .orderedSame }
        }
    }

    /// The size part of a variant, e.g. "M" from "M / Black".
    static func size(of variant: ListingOptions.Product.Variant, in product: ListingOptions.Product) -> String? {
        guard let values = variant.options, !values.isEmpty else { return nil }
        let names = product.options?.map { $0.name.lowercased() } ?? []
        if let index = names.firstIndex(where: { $0.contains("size") }), index < values.count {
            return values[index]
        }
        return values.count == 1 ? values[0] : nil
    }

    // MARK: Checkout

    func submit(api: APIClient, app: AppModel) async -> Look? {
        guard canSubmit else { return nil }
        isSubmitting = true
        errorMessage = nil
        defer { isSubmitting = false }
        let items = pieces.filter(\.included).map { piece in
            let size = piece.size.trimmingCharacters(in: .whitespacesAndNewlines)
            return LookRequest.Item(itemId: piece.id, listingUrl: piece.listingURL, variantId: piece.variantID,
                                    size: size.isEmpty ? nil : size, quantity: 1)
        }
        do {
            let look = try await api.createLook(LookRequest(pinId: pin.id, addressId: addressID, items: items))
            await rememberSizes(api: api)
            return look
        } catch is CancellationError {
            return nil
        } catch {
            if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            return nil
        }
    }

    // MARK: Buying through Fitkit

    /// Places the order and returns it with its number.
    func buy(api: APIClient, app: AppModel) async -> FitkitOrder? {
        guard canBuy else { return nil }
        isSubmitting = true
        errorMessage = nil
        defer { isSubmitting = false }
        let name = trimmedBuyerName
        UserDefaults.standard.set(name, forKey: Self.buyerNameKey)
        let items = pieces.filter(\.included).map { piece in
            OrderRequest.Item(itemId: piece.id, listingUrl: piece.listingURL, size: orderSize(for: piece),
                              quantity: 1, comment: nil)
        }
        do {
            let order = try await api.createOrder(
                OrderRequest(pinId: pin.id, name: name, idempotencyKey: orderKey, items: items))
            orderKey = UUID().uuidString
            await rememberSizes(api: api)
            return order
        } catch is CancellationError {
            return nil
        } catch {
            if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            return nil
        }
    }

    /// The size to send: the picked store variant's size, else the free-text one.
    private func orderSize(for piece: Piece) -> String? {
        var size = piece.size.trimmingCharacters(in: .whitespacesAndNewlines)
        if let product = product(for: piece), let id = piece.variantID,
           let variant = product.variants.first(where: { $0.id == id }) {
            size = Self.size(of: variant, in: product) ?? variant.title
        }
        return size.isEmpty ? nil : String(size.prefix(Self.maxSizeLength))
    }

    /// Saves the sizes used in this checkout for next time. Best effort.
    private func rememberSizes(api: APIClient) async {
        var sizes = rememberedSizes
        for piece in pieces where piece.included && SizeCategory(rawValue: piece.item.category) != nil {
            var size = piece.size.trimmingCharacters(in: .whitespaces)
            if let product = product(for: piece), let id = piece.variantID,
               let variant = product.variants.first(where: { $0.id == id }) {
                size = Self.size(of: variant, in: product) ?? size
            }
            if !size.isEmpty { sizes[piece.item.category] = size }
        }
        guard sizes != rememberedSizes else { return }
        if let saved = try? await api.setSizes(sizes) { rememberedSizes = saved }
    }
}
