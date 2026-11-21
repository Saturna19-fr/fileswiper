import SwiftUI

struct SettingsView: View {
    @EnvironmentObject private var settings: AppSettings

    @State private var status: ServerStatus?
    @State private var message: String?
    @State private var busy = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("192.168.1.20:8787", text: $settings.host)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                    TextField("Code", text: $settings.token)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                } header: {
                    Text("PC")
                } footer: {
                    Text("L'adresse et le code s'affichent au lancement du serveur sur le PC.")
                }

                Section {
                    Button("Tester la connexion") { run { try await $0.status() } }
                    Button("Rescanner le dossier") {
                        run {
                            try await $0.rescan()
                            return try await $0.status()
                        }
                    }
                }
                .disabled(busy || settings.client == nil)

                if let message {
                    Section { Text(message).foregroundStyle(.red) }
                }
                if let status {
                    Section("Serveur « \(status.name) »") {
                        row("À trier", status.source)
                        row("Validé", status.validated)
                        row("Supprimé", status.trash)
                        row("Restants", "\(status.pending)")
                        row("Dans Validé", "\(status.validatedTotal)")
                        row("Groupes de doublons", "\(status.duplicateGroups)")
                    }
                }
            }
            .navigationTitle("Réglages")
        }
    }

    private func row(_ title: String, _ value: String) -> some View {
        LabeledContent(title) {
            Text(value).font(.footnote).multilineTextAlignment(.trailing)
        }
    }

    private func run(_ work: @escaping (APIClient) async throws -> ServerStatus) {
        guard let client = settings.client else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                status = try await work(client)
                message = nil
            } catch {
                status = nil
                message = error.localizedDescription
            }
        }
    }
}
