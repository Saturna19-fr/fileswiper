import SwiftUI

struct FileCardView: View {
    let item: FileItem
    let client: APIClient

    @State private var image: UIImage?
    @State private var loaded = false

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            preview
            info
        }
        .background(Color(.systemBackground))
        .clipShape(RoundedRectangle(cornerRadius: 22, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 22, style: .continuous).stroke(Color(.separator), lineWidth: 0.5))
        .shadow(color: .black.opacity(0.15), radius: 8, y: 4)
        .task(id: item.id) {
            image = await PreviewStore.shared.thumbnail(for: item, client: client)
            loaded = true
        }
    }

    private var preview: some View {
        ZStack {
            Color(.secondarySystemBackground)
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .scaledToFit()
            } else if loaded {
                VStack(spacing: 10) {
                    Image(systemName: item.icon).font(.system(size: 64))
                    Text(item.ext.isEmpty ? "FICHIER" : item.ext.uppercased()).font(.headline)
                    Text("Toucher pour ouvrir").font(.caption)
                }
                .foregroundStyle(.secondary)
            } else {
                ProgressView()
            }
            if item.kind == "video" {
                Image(systemName: "play.circle.fill")
                    .font(.system(size: 60))
                    .foregroundStyle(.white.opacity(0.9))
                    .shadow(radius: 6)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .clipped()
    }

    private var info: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(item.name)
                .font(.title3.weight(.semibold))
                .lineLimit(2)
            Label(item.path, systemImage: "folder")
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .lineLimit(3)
                .truncationMode(.middle)
            HStack {
                Label(item.modifiedText, systemImage: "clock")
                Spacer()
                Text(item.sizeText)
            }
            .font(.subheadline)
            .foregroundStyle(.secondary)

            if !item.validatedCopies.isEmpty {
                badge("Déjà dans Validé : \(item.validatedCopies[0])", icon: "checkmark.seal.fill", color: .red)
            }
            if item.dupCount > 0 {
                badge(item.dupCount == 1 ? "1 autre copie à trier" : "\(item.dupCount) autres copies à trier",
                      icon: "doc.on.doc.fill", color: .orange)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func badge(_ text: String, icon: String, color: Color) -> some View {
        Label(text, systemImage: icon)
            .font(.caption.weight(.semibold))
            .lineLimit(1)
            .truncationMode(.middle)
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(color.opacity(0.15), in: Capsule())
            .foregroundStyle(color)
    }
}
