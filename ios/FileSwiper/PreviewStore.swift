import AVFoundation
import QuickLookThumbnailing
import UIKit

/// Builds card previews: server thumbnail for images, a frame for videos,
/// and a Quick Look thumbnail of the downloaded file for everything else.
actor PreviewStore {
    static let shared = PreviewStore()

    /// Above this size a file is only downloaded when the user opens it.
    static let autoDownloadLimit: Int64 = 40 * 1024 * 1024

    private var thumbs: [String: UIImage] = [:]
    private var files: [String: URL] = [:]
    private let directory: URL

    init() {
        directory = FileManager.default.temporaryDirectory.appendingPathComponent("previews", isDirectory: true)
        try? FileManager.default.removeItem(at: directory)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    }

    func thumbnail(for item: FileItem, client: APIClient) async -> UIImage? {
        if let cached = thumbs[item.id] { return cached }
        var image: UIImage?
        switch item.kind {
        case "image":
            image = await serverThumb(item, client)
            if image == nil { image = await quickLookThumb(item, client) }
        case "video":
            image = await videoFrame(item, client)
        case "archive", "other":
            image = nil
        default:
            image = await quickLookThumb(item, client)
        }
        if let image {
            if thumbs.count > 60 { thumbs.removeAll() }
            thumbs[item.id] = image
        }
        return image
    }

    /// Downloads the file once so Quick Look can open it.
    func localFile(for item: FileItem, client: APIClient) async throws -> URL {
        if let url = files[item.id], FileManager.default.fileExists(atPath: url.path) { return url }
        let (tmp, response) = try await URLSession.shared.download(from: client.contentURL(id: item.id))
        guard (response as? HTTPURLResponse)?.statusCode == 200 else {
            throw APIError(status: 0, message: "Téléchargement impossible")
        }
        // One folder per file keeps the real name, which Quick Look shows as title.
        let folder = directory.appendingPathComponent(item.id, isDirectory: true)
        try? FileManager.default.removeItem(at: folder)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let destination = folder.appendingPathComponent(item.name)
        try FileManager.default.moveItem(at: tmp, to: destination)
        files[item.id] = destination
        return destination
    }

    func forget(_ id: String) {
        thumbs[id] = nil
        if let url = files.removeValue(forKey: id) {
            try? FileManager.default.removeItem(at: url.deletingLastPathComponent())
        }
    }

    private func serverThumb(_ item: FileItem, _ client: APIClient) async -> UIImage? {
        guard let (data, response) = try? await URLSession.shared.data(from: client.thumbURL(id: item.id)),
              (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
        return UIImage(data: data)
    }

    private func quickLookThumb(_ item: FileItem, _ client: APIClient) async -> UIImage? {
        guard item.size <= Self.autoDownloadLimit,
              let url = try? await localFile(for: item, client: client) else { return nil }
        let request = QLThumbnailGenerator.Request(
            fileAt: url, size: CGSize(width: 600, height: 600), scale: 2, representationTypes: .thumbnail)
        return try? await QLThumbnailGenerator.shared.generateBestRepresentation(for: request).uiImage
    }

    private func videoFrame(_ item: FileItem, _ client: APIClient) async -> UIImage? {
        let generator = AVAssetImageGenerator(asset: AVURLAsset(url: client.contentURL(id: item.id)))
        generator.appliesPreferredTrackTransform = true
        generator.maximumSize = CGSize(width: 1200, height: 1200)
        guard let frame = try? await generator.image(at: CMTime(seconds: 1, preferredTimescale: 600)) else { return nil }
        return UIImage(cgImage: frame.image)
    }
}
