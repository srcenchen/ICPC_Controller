import { Badge, Button, Card, Empty, PageHeader } from "@/components/ui";
import { timeAgo } from "@/lib/utils";
import { useApp } from "@/state";

export function Rooms() {
  const app = useApp();
  if (!app.cloud) {
    return (
      <div>
        <PageHeader title="机房连接" description="当前不是云端模式。单机直接管理本局域网；并机模式会主动连接云端。" />
        <Card>
          <h2 className="font-semibold">{app.deployment.room_name || "当前局域网"}</h2>
          <p className="mt-2 text-sm text-muted-foreground">云端连接：{app.connection || "未连接"}</p>
          <div className="mt-4 flex gap-2">
            <Button onClick={() => app.go("settings")}>配置部署模式</Button>
            <Button variant="outline" onClick={() => app.go("devices")}>管理本地设备</Button>
          </div>
        </Card>
      </div>
    );
  }
  return (
    <div>
      <PageHeader title="机房总览" description="云端汇总各机房中转。进入某个机房后，设备、命令、分发都作用在该机房。">
        <Button variant="outline" onClick={() => app.refreshRooms().then(() => app.toast("已刷新", "ok")).catch((err) => app.toast(err.message, "bad"))}>刷新</Button>
      </PageHeader>
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {app.rooms.map((room) => {
          const online = room.devices.filter((device) => device.connected).length;
          return (
            <Card key={room.id}>
              <div className="mb-2 flex items-center justify-between">
                <h2 className="font-semibold">{room.name}</h2>
                <Badge tone={room.online ? "ok" : "muted"}>{room.online ? "中转在线" : "中转离线"}</Badge>
              </div>
              <div className="text-3xl font-semibold tabular-nums">{online}<span className="text-base font-normal text-muted-foreground"> / {room.devices.length}</span></div>
              <p className="mt-1 text-xs text-muted-foreground">最后上报 {timeAgo(room.last_seen)}{room.transfer_phase ? " · " + room.transfer_phase : ""}</p>
              <Button className="mt-3" variant="outline" onClick={() => { app.setRoom(room.id); app.go("devices"); }}>进入机房</Button>
            </Card>
          );
        })}
      </div>
      {!app.rooms.length && <Empty>还没有机房连接。先在机房中转里填写云端地址和相同密钥。</Empty>}
    </div>
  );
}
