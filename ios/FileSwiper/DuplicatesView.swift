import SwiftUI

struct DuplicatesView: View {
    @EnvironmentObject private var settings: AppSettings

    @State private var groups: [DuplicateGroup] = []
    @State private var status: ServerStatus?
    @State private var error: String?
    @State private var loaded = false

    var body: some View {
        NavigationStack {
            Group {
                if let client = settings.client {
                    list(client)
                } else {
                    ContentUnavailableView("Serveur non configuré", systemImage: "wifi.slash")
                }
            }
            .navigationTitle("Doublons")
        }
    }

    private func list(_ client: APIClient) -> some View {
        List {
            if let status, status.scanning || status.hashing {
                Section {
                    ProgressView(value: Double(status.hashDone), total: Double(max(status.hashTotal, status.hashDone, 1))) {
                        Text(status.scanning ? "Scan du dossier…" : "Recherche des doublons…")
                    }
                }
            }
            if let error {
                Section { Text(error).foregroundStyle(.red) }
            }
            ForEach(groups) { group in
                Section {
                    ForEach(group.validatedCopies, id: \.self) { path in
                        Label(path, systemImage: "checkmark.seal.fill")
                            .font(.footnote)
                            .foregroundStyle(.green)
                    }
                    ForEach(group.files) { file in
                        HStack {
                            VStack(alignment: .leading, spacing: 3) {
                                Text(file.name).font(.subheadline.weight(.medium)).lineLimit(1)
                                Text(file.path).font(.caption2.monospaced()).foregroundStyle(.secondary)
                                    .lineLimit(2).truncationMode(.middle)
                                Text(file.modifiedText).font(.caption2).foregroundStyle(.secondary)
                            }
                            Spacer()
                            Button("Garder") { resolve(group, keep: file.id, client) }
                                .buttonStyle(.bordered)
                                .tint(.green)
                        }
                    }
                    if !group.validatedCopies.isEmpty {
                        Button(role: .destructive) {
                            resolve(group, keep: nil, client)
                        } label: {
                            Label(group.files.count == 1 ? "Supprimer la copie restante" : "Supprimer les \(group.files.count) copies restantes",
                                  systemImage: "trash")
                        }
                    }
                } header: {
                    Text("\(group.files.count + group.validatedCopies.count) copies · \(ByteCountFormatter.string(fromByteCount: group.size, countStyle: .file))")
                } footer: {
                    Text("« Garder » valide ce fichier et supprime les autres copies.")
                }
            }
        }
        .overlay {
            if loaded, groups.isEmpty, error == nil, status?.hashing != true, status?.scanning != true {
                ContentUnavailableView("Aucun doublon", systemImage: "checkmark.circle")
            }
        }
        .refreshable { await load(client) }
        .task(id: client.baseURL.absoluteString + client.token) { await load(client) }
        .onAppear { Task { await load(client) } }
    }

    private func load(_ client: APIClient) async {
        do {
            status = try await client.status()
            groups = try await client.duplicates()
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
        loaded = true
    }

    private func resolve(_ group: DuplicateGroup, keep: String?, _ client: APIClient) {
        Task {
            do {
                try await client.resolveDuplicates(hash: group.hash, keepId: keep)
                withAnimation { groups.removeAll { $0.id == group.id } }
            } catch {
                self.error = error.localizedDescription
            }
            await load(client)
        }
    }
}
