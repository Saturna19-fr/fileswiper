import Foundation

struct APIError: LocalizedError {
    let status: Int
    let message: String
    var errorDescription: String? { message }
}

/// Talks to the FileSwiper server running on the PC.
struct APIClient: Sendable {
    let baseURL: URL
    let token: String

    init?(host: String, token: String) {
        var text = host.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return nil }
        if !text.contains("://") { text = "http://" + text }
        guard var comps = URLComponents(string: text), comps.host?.isEmpty == false else { return nil }
        if comps.port == nil, comps.scheme == "http" { comps.port = 8787 }
        comps.path = ""
        comps.query = nil
        guard let url = comps.url else { return nil }
        self.baseURL = url
        self.token = token.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private static let session: URLSession = {
        let config = URLSessionConfiguration.default
        config.timeoutIntervalForRequest = 20
        config.waitsForConnectivity = false
        return URLSession(configuration: config)
    }()

    private static let decoder: JSONDecoder = {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .secondsSince1970
        return decoder
    }()

    private func url(_ path: String, query: [URLQueryItem] = []) -> URL {
        var comps = URLComponents(url: baseURL, resolvingAgainstBaseURL: false)!
        comps.path = "/api/" + path
        if !query.isEmpty { comps.queryItems = query }
        return comps.url!
    }

    private func send(_ method: String, _ path: String, query: [URLQueryItem] = [], body: [String: String]? = nil) async throws -> Data {
        var request = URLRequest(url: url(path, query: query))
        request.httpMethod = method
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        if let body {
            request.httpBody = try JSONEncoder().encode(body)
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        let (data, response) = try await Self.session.data(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            let message = (try? JSONDecoder().decode([String: String].self, from: data))?["error"]
            throw APIError(status: status, message: message ?? "Erreur serveur (\(status))")
        }
        return data
    }

    private func get<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        try Self.decoder.decode(T.self, from: try await send("GET", path, query: query))
    }

    func status() async throws -> ServerStatus {
        try await get("status")
    }

    func files(limit: Int) async throws -> FilesResponse {
        try await get("files", query: [URLQueryItem(name: "limit", value: String(limit))])
    }

    func decide(id: String, action: SwipeAction) async throws {
        _ = try await send("POST", "files/\(id)/decision", body: ["action": action.rawValue])
    }

    func undo() async throws -> UndoResponse {
        try Self.decoder.decode(UndoResponse.self, from: try await send("POST", "undo"))
    }

    func duplicates() async throws -> [DuplicateGroup] {
        let response: DuplicatesResponse = try await get("duplicates")
        return response.groups
    }

    func resolveDuplicates(hash: String, keepId: String?) async throws {
        _ = try await send("POST", "duplicates/\(hash)/resolve", body: ["keepId": keepId ?? ""])
    }

    func rescan() async throws {
        _ = try await send("POST", "rescan")
    }

    // Media URLs carry the token in the query: AVPlayer cannot set headers.
    func contentURL(id: String) -> URL {
        url("files/\(id)/content", query: [URLQueryItem(name: "token", value: token)])
    }

    func thumbURL(id: String, size: Int = 900) -> URL {
        url("files/\(id)/thumb", query: [
            URLQueryItem(name: "size", value: String(size)),
            URLQueryItem(name: "token", value: token),
        ])
    }
}
