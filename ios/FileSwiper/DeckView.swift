import AVKit
import QuickLook
import SwiftUI

struct DeckView: View {
    @EnvironmentObject private var settings: AppSettings
    @StateObject private var model = DeckModel()

    @State private var drag: CGSize = .zero
    @State private var flying = false
    @State private var previewURL: URL?
    @State private var videoItem: FileItem?
    @State private var opening = false

    private let threshold: CGFloat = 110

    var body: some View {
        NavigationStack {
            Group {
                if let client = settings.client {
                    content(client)
                        .task(id: client.baseURL.absoluteString + client.token) {
                            await model.refresh(client: client, reset: true)
                        }
                } else {
                    ContentUnavailableView(
                        "Serveur non configuré", systemImage: "wifi.slash",
                        description: Text("Renseigne l'adresse et le code du PC dans Réglages."))
                }
            }
            .navigationTitle("FileSwiper")
            .navigationBarTitleDisplayMode(.inline)
        }
    }

    private func content(_ client: APIClient) -> some View {
        VStack(spacing: 12) {
            header
            ZStack {
                if model.cards.isEmpty {
                    emptyState(client)
                }
                ForEach(Array(model.cards.prefix(3).enumerated()).reversed(), id: \.element.id) { index, item in
                    let top = index == 0
                    FileCardView(item: item, client: client)
                        .overlay(alignment: .top) {
                            if top { swipeLabel }
                        }
                        .scaleEffect(top ? 1 : 1 - CGFloat(index) * 0.04)
                        .offset(x: top ? drag.width : 0, y: top ? drag.height * 0.3 : CGFloat(index) * 10)
                        .rotationEffect(.degrees(top ? Double(drag.width / 22) : 0))
                        .allowsHitTesting(top)
                        .onTapGesture { open(item, client) }
                        .gesture(dragGesture(item, client))
                }
                if opening {
                    ProgressView().controlSize(.large).padding(24).background(.regularMaterial, in: Circle())
                }
            }
            .padding(.horizontal, 16)
            .frame(maxHeight: .infinity)

            if let error = model.error {
                Text(error)
                    .font(.footnote)
                    .foregroundStyle(.red)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal)
            }
            buttons(client)
        }
        .padding(.bottom, 8)
        .background(Color(.systemGroupedBackground))
        .onAppear {
            // Coming back from the duplicates tab: the queue may have changed.
            Task { await model.refresh(client: client, reset: true) }
        }
        .quickLookPreview($previewURL)
        .sheet(item: $videoItem) { item in
            VideoSheet(url: client.contentURL(id: item.id))
        }
    }

    private var header: some View {
        HStack {
            Label("\(model.pending) à trier", systemImage: "tray.full")
            Spacer()
            if let status = model.status {
                Label("\(status.kept)", systemImage: "checkmark.circle.fill").foregroundStyle(.green)
                Label("\(status.trashed)", systemImage: "trash.fill").foregroundStyle(.red)
            }
        }
        .font(.subheadline.weight(.medium))
        .padding(.horizontal, 20)
        .padding(.top, 8)
    }

    @ViewBuilder
    private var swipeLabel: some View {
        if abs(drag.width) > 30 {
            let keep = drag.width > 0
            Text(keep ? "GARDER" : "SUPPRIMER")
                .font(.title.weight(.heavy))
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .foregroundStyle(.white)
                .background(keep ? Color.green : Color.red, in: RoundedRectangle(cornerRadius: 10))
                .rotationEffect(.degrees(keep ? -12 : 12))
                .opacity(min(1, Double(abs(drag.width) / threshold)))
                .padding(.top, 40)
        }
    }

    private func emptyState(_ client: APIClient) -> some View {
        VStack(spacing: 14) {
            if model.loading || model.status?.scanning == true {
                ProgressView("Chargement…")
            } else {
                Image(systemName: model.error == nil ? "checkmark.seal" : "exclamationmark.triangle")
                    .font(.system(size: 56))
                    .foregroundStyle(.secondary)
                Text(model.error == nil ? "Plus rien à trier" : "Serveur injoignable")
                    .font(.title3.weight(.semibold))
                Button("Actualiser") {
                    Task { await model.refresh(client: client, reset: true) }
                }
                .buttonStyle(.bordered)
            }
        }
    }

    private func buttons(_ client: APIClient) -> some View {
        let top = model.cards.first
        return HStack(spacing: 22) {
            round("xmark", .red, big: true) { if let top { swipe(top, .delete, client) } }
                .disabled(top == nil)
            round("arrow.uturn.backward", .orange, big: false) { Task { await model.undo(client: client) } }
                .disabled(model.status?.canUndo == false && model.cards.isEmpty)
            round("arrow.right.to.line", .blue, big: false) { if let top { swipe(top, .skip, client) } }
                .disabled(top == nil)
            round("checkmark", .green, big: true) { if let top { swipe(top, .keep, client) } }
                .disabled(top == nil)
        }
    }

    private func round(_ icon: String, _ color: Color, big: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon)
                .font(.system(size: big ? 28 : 20, weight: .bold))
                .frame(width: big ? 68 : 50, height: big ? 68 : 50)
                .foregroundStyle(color)
                .background(Color(.systemBackground), in: Circle())
                .shadow(color: .black.opacity(0.12), radius: 5, y: 2)
        }
    }

    private func dragGesture(_ item: FileItem, _ client: APIClient) -> some Gesture {
        DragGesture()
            .onChanged { value in
                if !flying { drag = value.translation }
            }
            .onEnded { value in
                guard !flying else { return }
                if value.translation.width > threshold {
                    swipe(item, .keep, client)
                } else if value.translation.width < -threshold {
                    swipe(item, .delete, client)
                } else {
                    withAnimation(.spring(response: 0.3, dampingFraction: 0.7)) { drag = .zero }
                }
            }
    }

    /// Flies the top card off screen, then tells the server.
    private func swipe(_ item: FileItem, _ action: SwipeAction, _ client: APIClient) {
        guard !flying else { return }
        flying = true
        UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        let target: CGSize
        switch action {
        case .keep: target = CGSize(width: 700, height: drag.height)
        case .delete: target = CGSize(width: -700, height: drag.height)
        case .skip: target = CGSize(width: 0, height: -3000)
        }
        withAnimation(.easeOut(duration: 0.2)) { drag = target }
        Task {
            try? await Task.sleep(nanoseconds: 200_000_000)
            drag = .zero
            flying = false
            await model.decide(item, action, client: client)
        }
    }

    private func open(_ item: FileItem, _ client: APIClient) {
        if item.kind == "video" {
            videoItem = item
            return
        }
        guard !opening else { return }
        opening = true
        Task {
            defer { opening = false }
            do {
                previewURL = try await PreviewStore.shared.localFile(for: item, client: client)
            } catch {
                model.error = error.localizedDescription
            }
        }
    }
}

private struct VideoSheet: View {
    let url: URL
    @State private var player: AVPlayer?

    var body: some View {
        VideoPlayer(player: player)
            .ignoresSafeArea()
            .onAppear {
                let player = AVPlayer(url: url)
                self.player = player
                player.play()
            }
            .onDisappear { player?.pause() }
    }
}
