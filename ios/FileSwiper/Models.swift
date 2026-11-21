import Foundation

struct FileItem: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let name: String
    let relPath: String
    let path: String
    let size: Int64
    let modified: Date
    let ext: String
    let kind: String
    let dupCount: Int
    let validatedCopies: [String]

    var sizeText: String { ByteCountFormatter.string(fromByteCount: size, countStyle: .file) }
    var modifiedText: String { modified.formatted(date: .abbreviated, time: .shortened) }

    var icon: String {
        switch kind {
        case "image": return "photo"
        case "video": return "film"
        case "audio": return "waveform"
        case "pdf": return "doc.richtext"
        case "text": return "doc.plaintext"
        case "document": return "doc.text"
        case "archive": return "archivebox"
        default: return "doc"
        }
    }
}

struct FilesResponse: Codable, Sendable {
    let files: [FileItem]
    let pending: Int
}

struct UndoResponse: Codable, Sendable {
    let file: FileItem
    let action: String
}

struct DuplicateGroup: Codable, Identifiable, Sendable {
    let hash: String
    let size: Int64
    let files: [FileItem]
    let validatedCopies: [String]

    var id: String { hash }
}

struct DuplicatesResponse: Codable, Sendable {
    let groups: [DuplicateGroup]
}

struct ServerStatus: Codable, Sendable {
    let name: String
    let source: String
    let validated: String
    let trash: String
    let pending: Int
    let kept: Int
    let trashed: Int
    let validatedTotal: Int
    let scanning: Bool
    let hashing: Bool
    let hashDone: Int
    let hashTotal: Int
    let duplicateGroups: Int
    let canUndo: Bool
}

enum SwipeAction: String, Sendable {
    case keep, delete, skip
}
