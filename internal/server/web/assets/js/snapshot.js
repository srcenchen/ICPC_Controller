"use strict";

// Operation snapshots (offline queues). Once enabled, every broadcast
// operation is recorded and replayed to devices/rooms that join later.

var SNAPSHOT_KIND_LABELS = {
    command: "执行命令",
    distribute: "文件分发",
    wol: "批量唤醒",
    schedule: "电源计划",
    broadcast: "广播发布"
};

function snapshotPanelHTML() {
    return '<section class="settings-card" id="snapshot-card">' +
        '<h3>操作快照 · 离线队列 <span class="badge" id="snapshot-scope-badge">—</span></h3>' +
        '<p class="settings-desc">启用后，所有「对全体设备」的广播操作都会进入队列。中途离线或之后上线的设备会完整补执行这些操作；单个设备的操作不会入队。可随时手动移除队列中的命令或结束快照。</p>' +
        '<div id="snapshot-body"><span class="settings-desc">加载中…</span></div></section>';
}

function loadSnapshotPanel() {
    if (!$("#snapshot-body").length) return;
    $.getJSON("/api/snapshots", function(data) {
        renderSnapshotPanel(data || {});
    }).fail(function() {
        $("#snapshot-body").html('<span class="settings-desc">快照服务不可用</span>');
    });
}

function renderSnapshotPanel(data) {
    var active = data.active;
    var badge = $("#snapshot-scope-badge");
    if (badge.length) badge.text(data.scope === "cloud" ? "云端" : "本机房");
    var host = $("#snapshot-body");
    if (!host.length) return;
    if (!active) {
        host.html('<div class="ops-toolbar">' +
            '<input id="snapshot-name" placeholder="快照名称，例如：2026 校赛" maxlength="100" style="flex:1;min-width:220px">' +
            '<button class="btn btn-primary" onclick="startSnapshot()">启用操作快照</button></div>');
        return;
    }
    host.html('<div class="ops-toolbar">' +
        '<strong>' + escapeHtml(active.name) + '</strong>' +
        '<span class="badge badge-online">进行中</span>' +
        '<span class="settings-desc">开始于 ' + escapeHtml(active.created_at) + ' · 共 ' + active.op_count + ' 条操作</span>' +
        '<button class="btn btn-sm btn-outline" onclick="refreshSnapshotOps(' + active.id + ')">刷新队列</button>' +
        '<button class="btn btn-sm btn-danger" onclick="endSnapshot(' + active.id + ')">结束快照</button></div>' +
        '<div id="snapshot-ops" class="table-container"><table><thead><tr><th>#</th><th>类型</th><th>内容</th><th>时间</th><th>已送达目标</th><th>操作</th></tr></thead><tbody id="snapshot-ops-body"></tbody></table></div>');
    loadSnapshotOps(active.id);
}

function loadSnapshotOps(id) {
    $.getJSON("/api/snapshots/" + id + "/ops", function(ops) {
        renderSnapshotOps(id, ops || []);
    });
}

function refreshSnapshotOps(id) {
    loadSnapshotOps(id);
}

function renderSnapshotOps(id, ops) {
    var body = $("#snapshot-ops-body");
    if (!body.length) return;
    if (!ops.length) {
        body.html('<tr><td colspan="6" class="empty-state">队列为空。之后的广播操作会自动进入这里。</td></tr>');
        return;
    }
    body.html(ops.map(function(op) {
        return '<tr>' +
            '<td>#' + op.id + '</td>' +
            '<td>' + escapeHtml(SNAPSHOT_KIND_LABELS[op.kind] || op.kind) + '</td>' +
            '<td>' + escapeHtml(op.summary || "") + '</td>' +
            '<td>' + escapeHtml(op.created_at) + '</td>' +
            '<td>' + op.delivered + '</td>' +
            '<td><button class="btn btn-sm btn-danger" onclick="deleteSnapshotOp(' + id + ',' + op.id + ')">移除</button></td>' +
        '</tr>';
    }).join(""));
}

function startSnapshot() {
    var name = ($("#snapshot-name").val() || "").trim();
    if (!name) { showToast("请填写快照名称", "error"); return; }
    if (!confirm("启用操作快照后，所有全设备广播操作都会被记录，并补执行给之后上线的设备。是否继续？")) return;
    $.ajax({
        url: "/api/snapshots", method: "POST", contentType: "application/json",
        data: JSON.stringify({ name: name })
    }).done(function() {
        showToast("操作快照已启用", "success");
        loadSnapshotPanel();
    }).fail(function(request) {
        showToast((request.responseJSON && request.responseJSON.error) || "启用失败", "error");
    });
}

function endSnapshot(id) {
    if (!confirm("结束快照后，新上线设备将不再收到这些历史操作。是否结束？")) return;
    $.ajax({ url: "/api/snapshots/" + id + "/end", method: "POST" }).done(function() {
        showToast("快照已结束", "success");
        loadSnapshotPanel();
    });
}

function deleteSnapshotOp(snapshotId, opId) {
    if (!confirm("确定要从队列中移除这条操作吗？之后上线的设备将不再执行它。")) return;
    $.ajax({ url: "/api/snapshots/" + snapshotId + "/ops/" + opId, method: "DELETE" }).done(function() {
        showToast("已从队列移除", "success");
        loadSnapshotOps(snapshotId);
    });
}
