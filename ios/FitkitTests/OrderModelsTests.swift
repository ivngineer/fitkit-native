import Foundation
import Testing
@testable import Fitkit

@Suite struct OrderModelsTests {
    @Test func decodesAnOrderFromTheServer() throws {
        let json = """
        {"id":"o1","number":"#1000","status":"Замовлення підтверджено","photoUrl":"/media/pins/a.jpg","total":null,
         "items":[
          {"name":"Top","store":"Zara","size":"M","quantity":1,"photoUrl":"/media/crops/i.jpg","price":179900,"listingPrice":"","cancelled":false},
          {"name":"Bottoms","store":"Amazon","size":"—","quantity":2,"photoUrl":"","price":null,"listingPrice":"$64.00","cancelled":true}],
         "createdAt":"2026-11-25T14:56:07.691234567Z"}
        """
        let order = try APIClient.decoder.decode(FitkitOrder.self, from: Data(json.utf8))
        #expect(order.number == "#1000")
        #expect(order.total == nil)
        #expect(order.items.count == 2)
        #expect(order.items[0].price == 179900)
        #expect(order.items[1].displayPrice == "$64.00")
        #expect(order.items[1].cancelled)
    }

    @Test func formatsHryvniasWithoutKopiykyWhenWhole() {
        let text = Hryvnia.format(179900)
        #expect(text.contains("₴") || text.contains("грн"))
        #expect(!text.contains(",00"))
    }

    @Test func encodesTheRequestTheServerExpects() throws {
        let request = OrderRequest(pinId: "p1", name: "Аня", idempotencyKey: "k1", items: [
            OrderRequest.Item(itemId: "item-1", listingUrl: "https://s.example/x", size: nil, quantity: 1, comment: nil),
        ])
        let data = try APIClient.encoder.encode(request)
        let object = try #require(try JSONSerialization.jsonObject(with: data) as? [String: Any])
        let items = try #require(object["items"] as? [[String: Any]])
        #expect(object["pinId"] as? String == "p1")
        #expect(object["name"] as? String == "Аня")
        #expect(object["idempotencyKey"] as? String == "k1")
        #expect(items.first?["listingUrl"] as? String == "https://s.example/x")
        #expect(items.first?["size"] == nil)
    }
}
