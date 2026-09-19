import SwiftUI

/// Static explainer for shoppers using a forwarder to reach stores that
/// don't ship to Ukraine directly.
struct NPShoppingGuideView: View {
    var body: some View {
        List {
            Section {
                Text("NP Shopping is Nova Poshta's forwarding service. It gives you personal warehouse addresses in the US and Poland, so you can shop stores that don't ship to Ukraine.")
            } header: {
                Text("What It Is")
            }

            Section {
                Label("Register at npshopping.com and get your suite ID.", systemImage: "1.circle")
                Label("Copy your US and Poland addresses, with your suite ID, into Fitkit as Forwarding Addresses.", systemImage: "2.circle")
                Label("Fitkit sends each store's order to the matching warehouse automatically.", systemImage: "3.circle")
                Label("Pay Nova Poshta delivery when you pick up your parcel, from about $3 per 0.5 kg, about 5–10 days from the warehouse.", systemImage: "4.circle")
                Link(destination: URL(string: "https://npshopping.com")!) {
                    Label("Open npshopping.com", systemImage: "arrow.up.right.square")
                }
            } header: {
                Text("How It Works")
            }

            Section {
                Text("Parcels over €150 pay 10% duty plus 20% VAT on the excess. Stores don't accept returns shipped from Ukraine.")
            } header: {
                Text("Before You Order")
            } footer: {
                Text("Fitkit doesn't create Nova Poshta accounts for you — register directly with NP Shopping.")
            }
        }
        .navigationTitle("Shipping to Ukraine")
        .navigationBarTitleDisplayMode(.inline)
    }
}
