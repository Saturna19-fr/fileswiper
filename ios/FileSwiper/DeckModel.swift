import SwiftUI

final class AppSettings: ObservableObject {
    @AppStorage("host") var host = ""
    @AppStorage("token") var token = ""

    var client: APIClient? { APIClient(host: host, token: token) }
}

@MainActor
final class DeckModel: ObservableObject {
    @Published var cards: [FileItem] = []
    @Published var pending = 0
    @Published var status: ServerStatus?
    @Published var error: String?
    @Published var loading = false

    /// Ids already swiped here: a refresh racing with a decision must not bring them back.
    private var hidden: Set<String> = []

    func refresh(client: APIClient, reset: Bool = false) async {
        loading = true
        defer { loading = false }
        do {
            let response = try await client.files(limit: (reset ? 0 : cards.count) + 20)
            let fresh = response.files.filter { !hidden.contains($0.id) }
            if reset {
                cards = fresh
            } else {
                let known = Set(cards.map(\.id))
                cards.append(contentsOf: fresh.filter { !known.contains($0.id) })
            }
            pending = response.pending
            error = nil
            status = try? await client.status()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func decide(_ item: FileItem, _ action: SwipeAction, client: APIClient) async {
        cards.removeAll { $0.id == item.id }
        hidden.insert(item.id)
        do {
            try await client.decide(id: item.id, action: action)
            if action == .skip {
                hidden.remove(item.id)
            } else {
                pending = max(0, pending - 1)
                await PreviewStore.shared.forget(item.id)
            }
            error = nil
        } catch let apiError as APIError where apiError.status == 404 || apiError.status == 410 {
            // Already handled elsewhere (duplicates tab, or removed on the PC).
        } catch {
            hidden.remove(item.id)
            cards.insert(item, at: 0)
            self.error = error.localizedDescription
            return
        }
        if cards.count < 6 { await refresh(client: client) }
    }

    func undo(client: APIClient) async {
        do {
            let response = try await client.undo()
            hidden.remove(response.file.id)
            cards.removeAll { $0.id == response.file.id }
            cards.insert(response.file, at: 0)
            pending += 1
            error = nil
            status = try? await client.status()
        } catch {
            self.error = error.localizedDescription
        }
    }
}
