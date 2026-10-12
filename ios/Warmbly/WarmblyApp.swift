import SwiftUI

@main
struct WarmblyApp: App {
    @UIApplicationDelegateAdaptor(PushAppDelegate.self) private var pushDelegate
    @State private var env = AppEnvironment()
    @AppStorage(AppearancePreference.storageKey) private var appearance = AppearancePreference.standard
    @Environment(\.scenePhase) private var scenePhase

    var body: some Scene {
        WindowGroup {
            RootView()
                .environment(env)
                .tint(WTheme.ink)
                .preferredColorScheme(appearance.colorScheme)
        }
        .onChange(of: scenePhase) { _, phase in
            if phase == .active { env.realtime.nudge() }
        }
    }
}
