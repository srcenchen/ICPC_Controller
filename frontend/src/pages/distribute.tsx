import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { formatBytes, statusLabel } from "@/lib/utils";
import type { DistFile, DistributeTask } from "@/lib/types";
import { Badge, Button, Card, Empty, Input, PageHeader, statusTone } from "@/components/ui";
import { TargetPicker } from "@/components/targets";
import { FleetTable, Jobs, queueFleet, useFleetDevices } from "@/components/fleet";
import { useApp, useDevices } from "@/state";

export function Distribute() {
  const app = useApp();
  const fleet = useFleetDevices();
  const { devices } = useDevices();
  const [files, setFiles] = useState<DistFile[]>([]);
  const [pickedFiles, setPickedFiles] = useState<string[]>([]);
  const [task, setTask] = useState<DistributeTask | null>(null);
  const [saveDir, setSaveDir] = useState("/tmp/icpc-downloads");
  const [postCmd, setPostCmd] = useState("");
  const [serverIP, setServerIP] = useState("");
  const [mode, setMode] = useState<"online" | "picked">("online");
  const [picked, setPicked] = useState<number[]>([]);
  const [progress, setProgress] = useState("");

  async function loadFiles() {
    setFiles(await api<DistFile[]>("/api/distribution/files"));
  }
  async function loadTask() {
    if (app.cloudAll) return;
    const next = await api<DistributeTask>("/api/distribution/status");
    setTask(next);
    if (next?.suggested_ip && !serverIP) setServerIP(next.suggested_ip);
  }
  useEffect(() => { loadFiles().catch(() => {}); loadTask().catch(() => {}); }, [app.roomID, app.cloudAll]);
  useEffect(() => {
    if (task?.status !== "running") return;
    const timer = window.setInterval(() => loadTask().catch(() => {}), 1000);
    return () => clearInterval(timer);
  }, [task?.status, app.roomID]);

  async function upload(file: File) {
    const body = new FormData();
    body.append("file", file);
    setProgress("正在上传 " + file.name);
    await api("/api/distribution/upload", { method: "POST", body });
    setProgress("上传完成");
    loadFiles();
  }

  const running = task && ["running", "completed", "stopped", "failed"].includes(task.status) && task.files;
  const progresses = Object.values(task?.progresses || {});

  return (
    <div className="space-y-4">
      <PageHeader title={app.cloudAll ? "云端文件库" : "文件分发"} description={app.cloudAll ? "云端保存一份。各机房拉取并校验后，再在机房内分发到选手机。" : "选中文件和目标后一次开始。空目标表示当前全部在线设备。"}>
        <label className="inline-flex h-9 cursor-pointer items-center rounded-md border border-border px-3 text-sm">
          上传文件
          <input type="file" className="hidden" onChange={(event) => { const file = event.target.files?.[0]; if (file) upload(file).catch((err) => app.toast(err.message, "bad")); event.target.value = ""; }} />
        </label>
      </PageHeader>
      {progress && <p className="text-sm text-muted-foreground">{progress}</p>}
      <Card>
        <div className="table-wrap max-h-64">
          <table className="data">
            <thead><tr><th></th><th>文件</th><th>大小</th><th>时间</th></tr></thead>
            <tbody>
              {files.map((file) => (
                <tr key={file.name}>
                  <td><input type="checkbox" checked={pickedFiles.includes(file.name)} onChange={() => setPickedFiles((list) => list.includes(file.name) ? list.filter((item) => item !== file.name) : [...list, file.name])} /></td>
                  <td className="font-medium">{file.name}</td>
                  <td>{formatBytes(file.size)}</td>
                  <td className="text-xs">{file.mod_time}</td>
                </tr>
              ))}
              {!files.length && <tr><td colSpan={4}><Empty>还没有文件</Empty></td></tr>}
            </tbody>
          </table>
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => setPickedFiles(files.map((file) => file.name))}>全选</Button>
          <Button size="sm" variant="ghost" onClick={() => setPickedFiles([])}>取消</Button>
          <Button size="sm" variant="destructive" onClick={async () => {
            if (!pickedFiles.length || !(await app.confirm("从文件库删除选中文件？已发到机器上的副本不受影响。"))) return;
            await api("/api/distribution/delete", { method: "POST", body: JSON.stringify({ filenames: pickedFiles }) });
            setPickedFiles([]);
            loadFiles();
          }}>删除选中</Button>
        </div>
      </Card>
      {!running && (
        <>
          {app.cloudAll ? <FleetTable /> : (
            <Card className="h-[320px]"><TargetPicker devices={devices} mode={mode} onMode={setMode} selected={picked} onToggle={(id) => setPicked((list) => list.includes(id) ? list.filter((item) => item !== id) : [...list, id])} onSet={setPicked} /></Card>
          )}
          <Card>
            <div className="grid gap-3 md:grid-cols-2">
              <label className="text-xs text-muted-foreground">保存目录<Input value={saveDir} onChange={(event) => setSaveDir(event.target.value)} /></label>
              {!app.cloudAll && <label className="text-xs text-muted-foreground">宣告 IP<Input value={serverIP} onChange={(event) => setServerIP(event.target.value)} placeholder="留空自动探测" /></label>}
              <label className="text-xs text-muted-foreground md:col-span-2">全部文件完成后执行<Input value={postCmd} onChange={(event) => setPostCmd(event.target.value)} placeholder="可选" /></label>
            </div>
            <Button className="mt-3" onClick={async () => {
              if (!pickedFiles.length) return app.toast("请选择文件", "bad");
              if (app.cloudAll) {
                if (!fleet.fleet.length) return app.toast("请选择设备", "bad");
                if (!(await app.confirm(`向 ${fleet.fleet.length} 台设备分发 ${pickedFiles.length} 个文件？`))) return;
                await queueFleet(fleet.fleet, "distribute", { files: pickedFiles, save_dir: saveDir, post_cmd: postCmd });
                app.toast("已入队", "ok");
                return;
              }
              const ids = mode === "picked" ? picked : [];
              if (mode === "picked" && !ids.length) return app.toast("请指定设备，或改成全部在线", "bad");
              await api("/api/distribution/start", { method: "POST", body: JSON.stringify({ files: pickedFiles, save_dir: saveDir, post_cmd: postCmd, server_ip: serverIP, target_ids: ids }) });
              app.toast("分发已开始", "ok");
              loadTask();
            }}>开始分发</Button>
          </Card>
        </>
      )}
      {running && task && (
        <Card>
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <Badge tone={statusTone(task.status)}>{statusLabel(task.status)}</Badge>
            <span className="text-sm">{task.active_file || "准备中"} · {(task.active_idx || 0) + 1}/{task.files?.length || 0}</span>
            {task.status === "running" && <Button size="sm" variant="destructive" onClick={() => api("/api/distribution/stop", { method: "POST" }).then(loadTask)}>停止</Button>}
            {task.status !== "running" && <Button size="sm" variant="outline" onClick={() => api("/api/distribution/reset", { method: "POST" }).then(() => setTask(null))}>返回准备</Button>}
            <Button size="sm" variant="outline" onClick={() => api("/api/distribution/retry", { method: "POST", body: JSON.stringify({ device_id: 0 }) }).then(() => app.toast("已补传未完成的设备", "ok"))}>补传缺失</Button>
          </div>
          <div className="table-wrap max-h-80">
            <table className="data">
              <thead><tr><th>设备</th><th>进度</th><th>状态</th><th></th></tr></thead>
              <tbody>
                {progresses.map((item) => (
                  <tr key={item.device_id}>
                    <td>#{item.device_id} {item.hostname}</td>
                    <td className="w-48"><div className="h-2 overflow-hidden rounded bg-muted"><div className="h-full bg-primary" style={{ width: Math.min(100, item.percentage || 0) + "%" }} /></div></td>
                    <td>{statusLabel(item.status)}{item.error ? " · " + item.error : ""}</td>
                    <td>{item.status === "failed" && <Button size="sm" variant="ghost" onClick={() => api("/api/distribution/retry", { method: "POST", body: JSON.stringify({ device_id: item.device_id }) })}>重试</Button>}</td>
                  </tr>
                ))}
                {!progresses.length && <tr><td colSpan={4}><Empty>等待设备回报</Empty></td></tr>}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      {app.cloudAll && <Jobs />}
    </div>
  );
}
