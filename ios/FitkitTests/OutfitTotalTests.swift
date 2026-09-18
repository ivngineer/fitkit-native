import Foundation
import Testing
@testable import Fitkit

@Suite struct OutfitTotalTests {
    private func result(_ listings: [String]) throws -> AnalysisResult {
        let items = listings.enumerated().map { index, listing in
            """
            {"id":"i\(index)","label":"Piece","category":"top","box":{"x":0,"y":0,"width":1,"height":1},"listings":[\(listing)]}
            """
        }
        let json = """
        {"provider":"demo","demo":true,"generatedAt":"2026-09-17T14:49:30Z","items":[\(items.joined(separator: ","))]}
        """
        return try APIClient.decoder.decode(AnalysisResult.self, from: Data(json.utf8))
    }

    private func listing(_ price: String?, _ value: Double?) -> String {
        var fields = [#""title":"T","merchant":"M","url":"https://s.example/\#(UUID().uuidString)""#]
        if let price { fields.append(#""price":"\#(price)""#) }
        if let value { fields.append(#""priceValue":\#(value)"#) }
        return "{" + fields.joined(separator: ",") + "}"
    }

    @Test func sumsPricedPiecesWithoutDecimals() throws {
        let look = try result([listing("$89.50", 89.5), listing("$20.20", 20.2), listing(nil, nil), ""])
        #expect(look.outfitTotal == "$110")
    }

    @Test func readsThePriceStringWhenThereIsNoValue() throws {
        let look = try result([listing("€1,200.00", nil), listing("€45", nil)])
        #expect(look.outfitTotal == "€" + 1245.formatted())
    }

    @Test func addsMixedCurrenciesAsPlainNumbers() throws {
        let look = try result([listing("$10", 10), listing("€5", 5)])
        #expect(look.outfitTotal == "$15")
    }

    @Test func hidesTheTotalWhenNothingHasAPrice() throws {
        #expect(try result([listing(nil, nil), ""]).outfitTotal == nil)
    }
}
