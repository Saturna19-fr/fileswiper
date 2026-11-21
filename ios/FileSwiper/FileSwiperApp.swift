import SwiftUI

@main
struct FileSwiperApp: App {
    @StateObject private var settings = AppSettings()

    var body: some Scene {
        WindowGroup {
            TabView {
                DeckView()
                    .tabItem { Label("Trier", systemImage: "rectangle.stack") }
                DuplicatesView()
                    .tabItem { Label("Doublons", systemImage: "doc.on.doc") }
                SettingsView()
                    .tabItem { Label("Réglages", systemImage: "gear") }
            }
            .environmentObject(settings)
        }
    }
}
