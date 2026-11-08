import type { DistributeTask } from "@/lib/types";

export const distributionIssues = new Set(["failed", "stalled", "cancelled"]);

export function DistributionProgress({ task }: { task: DistributeTask }) {
  const devices = Object.values(task.progresses || {});
  const completed = devices.filter((device) => device.status === "completed").length;
  const issues = devices.filter((device) => distributionIssues.has(device.status)).length;
  const average = devices.length
    ? devices.reduce((sum, device) => sum + Math.max(0, Math.min(100, device.percentage || 0)), 0) / devices.length
    : 0;
  return (
    <div className="space-y-1">
      <p className="break-all text-sm">{task.active_file || "准备中"}{task.files?.length ? ` · 第 ${(task.active_idx ?? 0) + 1}/${task.files.length} 个文件` : ""}</p>
      <div role="progressbar" aria-label="设备平均进度" aria-valuenow={Math.round(average)} className="h-2 overflow-hidden rounded bg-muted">
        <div className="h-full bg-primary" style={{ width: `${average}%` }} />
      </div>
      <p className="text-xs text-muted-foreground">平均进度 {average.toFixed(1)}% · 完成 {completed}/{devices.length} 台{issues ? ` · 待处理 ${issues} 台` : ""}</p>
    </div>
  );
}
