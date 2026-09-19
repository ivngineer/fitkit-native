import SwiftUI

struct AddressesView: View {
    @Environment(AppModel.self) private var app
    @State private var addresses: [Address] = []
    @State private var isLoading = true
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if isLoading {
                ProgressView()
            } else if addresses.isEmpty {
                ContentUnavailableView {
                    Label("No Addresses", systemImage: "shippingbox")
                } description: {
                    Text("Add where Fitkit should ship your orders.")
                } actions: {
                    NavigationLink {
                        AddressEditorView(address: nil, onSave: add)
                    } label: {
                        Text("Add Address")
                    }
                    .accessibilityIdentifier("addresses.emptyAdd")
                }
            } else {
                List {
                    Section {
                        ForEach(addresses) { address in
                            NavigationLink {
                                AddressEditorView(address: address, onSave: { update(with: $0) })
                            } label: {
                                AddressRow(address: address)
                            }
                        }
                        .onDelete(perform: deleteAddress)
                    } footer: {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                    }
                    Section {
                        NavigationLink {
                            NPShoppingGuideView()
                        } label: {
                            Text("Shipping to Ukraine?")
                        }
                    }
                }
                .accessibilityIdentifier("addresses.list")
            }
        }
        .navigationTitle("Addresses")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                NavigationLink {
                    AddressEditorView(address: nil, onSave: add)
                } label: {
                    Image(systemName: "plus")
                }
                .accessibilityIdentifier("addresses.add")
            }
        }
        .task { await load() }
    }

    private func load() async {
        do {
            addresses = try await app.api.addresses()
        } catch {
            if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
        }
        isLoading = false
    }

    private func add(_ address: Address) {
        addresses.append(address)
    }

    private func update(with address: Address) {
        if let index = addresses.firstIndex(where: { $0.id == address.id }) {
            addresses[index] = address
        }
    }

    private func deleteAddress(at offsets: IndexSet) {
        let removed = offsets.map { addresses[$0] }
        addresses.remove(atOffsets: offsets)
        Task {
            for address in removed {
                do {
                    try await app.api.deleteAddress(id: address.id)
                } catch {
                    if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
                }
            }
        }
    }
}

private struct AddressRow: View {
    let address: Address

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(address.title)
                    .font(.headline)
                if address.isDefault {
                    Text("Default")
                        .font(.caption2)
                        .fontWeight(.semibold)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.tint.opacity(0.15), in: .capsule)
                        .foregroundStyle(.tint)
                }
            }
            Text(address.summary)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
        .accessibilityElement(children: .combine)
    }
}

struct AddressEditorView: View {
    let address: Address?
    var onSave: (Address) -> Void

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var kind: Address.Kind = .home
    @State private var label = ""
    @State private var country = ""
    @State private var firstName = ""
    @State private var lastName = ""
    @State private var line1 = ""
    @State private var line2 = ""
    @State private var city = ""
    @State private var region = ""
    @State private var zip = ""
    @State private var phone = ""
    @State private var npBranchNumber = ""
    @State private var forwarder: Forwarder = .npShopping
    @State private var suiteId = ""
    @State private var finalCountry = "UA"
    @State private var isDefault = false
    @State private var isSaving = false
    @State private var errorMessage: String?

    init(address: Address?, onSave: @escaping (Address) -> Void) {
        self.address = address
        self.onSave = onSave
        if let address {
            _kind = State(initialValue: address.kind)
            _label = State(initialValue: address.label)
            _country = State(initialValue: address.country)
            _firstName = State(initialValue: address.fields.firstName)
            _lastName = State(initialValue: address.fields.lastName)
            _line1 = State(initialValue: address.fields.line1)
            _line2 = State(initialValue: address.fields.line2)
            _city = State(initialValue: address.fields.city)
            _region = State(initialValue: address.fields.region)
            _zip = State(initialValue: address.fields.zip)
            _phone = State(initialValue: address.fields.phone)
            _npBranchNumber = State(initialValue: address.fields.npBranch)
            _forwarder = State(initialValue: Forwarder(rawValue: address.forwarder) ?? .npShopping)
            _suiteId = State(initialValue: address.fields.suiteId)
            _finalCountry = State(initialValue: address.finalCountry.isEmpty ? "UA" : address.finalCountry)
            _isDefault = State(initialValue: address.isDefault)
        } else {
            _kind = State(initialValue: .home)
        }
    }

    var body: some View {
        Form {
            Section {
                Picker("Type", selection: $kind) {
                    ForEach(Address.Kind.allCases) { kind in
                        Text(kind.title).tag(kind)
                    }
                }
                TextField("Label (optional)", text: $label)
                TextField("Country", text: countryBinding)
                    .textInputAutocapitalization(.characters)
                    .autocorrectionDisabled()
            }

            Section {
                TextField("First Name", text: $firstName)
                    .textContentType(.givenName)
                TextField("Last Name", text: $lastName)
                    .textContentType(.familyName)
                if kind != .npBranch {
                    TextField("Phone (optional)", text: $phone)
                        .textContentType(.telephoneNumber)
                        .keyboardType(.phonePad)
                }
            }

            if kind == .home || kind == .forwarder {
                Section {
                    TextField("Street Address", text: $line1)
                        .textContentType(.streetAddressLine1)
                    TextField("Apt, Suite, etc. (optional)", text: $line2)
                        .textContentType(.streetAddressLine2)
                    TextField("City", text: $city)
                        .textContentType(.addressCity)
                    TextField("Region (optional)", text: $region)
                    TextField("Postal Code", text: $zip)
                        .textContentType(.postalCode)
                        .keyboardType(.numbersAndPunctuation)
                }
            }

            if kind == .npBranch {
                Section {
                    TextField("City", text: $city)
                        .textContentType(.addressCity)
                    TextField("Branch Number", text: $npBranchNumber)
                        .keyboardType(.numberPad)
                    TextField("Phone", text: $phone)
                        .textContentType(.telephoneNumber)
                        .keyboardType(.phonePad)
                }
            }

            if kind == .forwarder {
                Section {
                    Picker("Forwarder", selection: $forwarder) {
                        ForEach(Forwarder.allCases) { forwarder in
                            Text(forwarder.title).tag(forwarder)
                        }
                    }
                    TextField("Suite / Customer ID", text: $suiteId)
                    TextField("Delivers To (country code)", text: finalCountryBinding)
                        .textInputAutocapitalization(.characters)
                        .autocorrectionDisabled()
                } footer: {
                    Text("Your personal warehouse address from the forwarder. Stores that don't ship to Ukraine send here.")
                }
            }

            Section {
                Toggle("Use as Default", isOn: $isDefault)
            }

            if let errorMessage {
                Section {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
        }
        .navigationTitle(address == nil ? "New Address" : "Edit Address")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                if isSaving {
                    ProgressView()
                } else {
                    Button("Save", action: save)
                        .disabled(!canSave)
                        .accessibilityIdentifier("addresses.editor.save")
                }
            }
        }
        .onChange(of: kind) {
            if address == nil, kind == .npBranch, country.isEmpty {
                country = "UA"
            }
        }
    }

    private var countryBinding: Binding<String> {
        Binding(
            get: { country },
            set: { country = String($0.uppercased().prefix(2)) }
        )
    }

    private var finalCountryBinding: Binding<String> {
        Binding(
            get: { finalCountry },
            set: { finalCountry = String($0.uppercased().prefix(2)) }
        )
    }

    private var canSave: Bool {
        guard country.trimmingCharacters(in: .whitespaces).count == 2 else { return false }
        guard !firstName.trimmingCharacters(in: .whitespaces).isEmpty,
              !lastName.trimmingCharacters(in: .whitespaces).isEmpty else { return false }
        switch kind {
        case .home:
            return !line1.trimmingCharacters(in: .whitespaces).isEmpty
                && !city.trimmingCharacters(in: .whitespaces).isEmpty
                && !zip.trimmingCharacters(in: .whitespaces).isEmpty
        case .npBranch:
            return !city.trimmingCharacters(in: .whitespaces).isEmpty
                && !npBranchNumber.trimmingCharacters(in: .whitespaces).isEmpty
                && !phone.trimmingCharacters(in: .whitespaces).isEmpty
        case .forwarder:
            return !line1.trimmingCharacters(in: .whitespaces).isEmpty
                && !city.trimmingCharacters(in: .whitespaces).isEmpty
                && !zip.trimmingCharacters(in: .whitespaces).isEmpty
                && !suiteId.trimmingCharacters(in: .whitespaces).isEmpty
                && finalCountry.trimmingCharacters(in: .whitespaces).count == 2
        }
    }

    private func save() {
        isSaving = true
        Task {
            defer { isSaving = false }
            do {
                var result = address ?? Address()
                result.kind = kind
                result.label = label.trimmingCharacters(in: .whitespaces)
                result.country = country.trimmingCharacters(in: .whitespaces).uppercased()
                result.forwarder = kind == .forwarder ? forwarder.rawValue : ""
                result.finalCountry = kind == .forwarder ? finalCountry.trimmingCharacters(in: .whitespaces).uppercased() : ""
                result.fields.firstName = firstName.trimmingCharacters(in: .whitespaces)
                result.fields.lastName = lastName.trimmingCharacters(in: .whitespaces)
                result.fields.line1 = kind == .npBranch ? "" : line1.trimmingCharacters(in: .whitespaces)
                result.fields.line2 = kind == .npBranch ? "" : line2.trimmingCharacters(in: .whitespaces)
                result.fields.city = city.trimmingCharacters(in: .whitespaces)
                result.fields.region = kind == .npBranch ? "" : region.trimmingCharacters(in: .whitespaces)
                result.fields.zip = kind == .npBranch ? "" : zip.trimmingCharacters(in: .whitespaces)
                result.fields.phone = phone.trimmingCharacters(in: .whitespaces)
                result.fields.suiteId = kind == .forwarder ? suiteId.trimmingCharacters(in: .whitespaces) : ""
                result.fields.npBranch = kind == .npBranch ? npBranchNumber.trimmingCharacters(in: .whitespaces) : ""
                result.isDefault = isDefault

                let saved = address == nil
                    ? try await app.api.createAddress(result)
                    : try await app.api.updateAddress(result)
                onSave(saved)
                dismiss()
            } catch {
                if !app.handleUnauthorized(error) { errorMessage = error.localizedDescription }
            }
        }
    }
}
