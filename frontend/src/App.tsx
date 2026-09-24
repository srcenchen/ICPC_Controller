import { AppProvider, useApp } from "@/state";
import { Shell } from "@/components/shell";
import { Login } from "@/pages/login";
import { Dashboard } from "@/pages/dashboard";
import { Rooms } from "@/pages/rooms";
import { Devices } from "@/pages/devices";
import { Commands } from "@/pages/commands";
import { Network } from "@/pages/network";
import { Distribute } from "@/pages/distribute";
import { Power } from "@/pages/power";
import { Checkin } from "@/pages/checkin";
import { Screen } from "@/pages/screen";
import { Broadcast } from "@/pages/broadcast";
import { SettingsPage } from "@/pages/settings";

function Router() {
  const { page } = useApp();
  switch (page) {
    case "rooms": return <Rooms />;
    case "devices": return <Devices />;
    case "commands": return <Commands />;
    case "network": return <Network />;
    case "distribute": return <Distribute />;
    case "power": return <Power />;
    case "checkin": return <Checkin />;
    case "screen": return <Screen />;
    case "broadcast": return <Broadcast />;
    case "settings": return <SettingsPage />;
    default: return <Dashboard />;
  }
}

export function App() {
  if (location.pathname === "/login" || location.pathname === "/login.html") return <Login />;
  return (
    <AppProvider>
      <Shell>
        <Router />
      </Shell>
    </AppProvider>
  );
}
