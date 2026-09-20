// ICPC Remote Control - Power management (Wake-on-LAN + scheduled shutdown/reboot)
"use strict";

var _powerDevices = [];

function loadPower() {
    var html = '' +
        '<div class="page-header"><h1>电源管理</h1>' +
        '<p class="page-sub">远程唤醒（Wake-on-LAN）与定时关机/重启。WoL 需在选手机 BIOS/网卡启用，且记录的 MAC 为有线网卡。</p></div>' +

        '<div class="card" style="margin-bottom:16px">' +
        '  <div class="card-head"><h3>立即操作</h3></div>' +
        '  <div class="power-actions">' +
        '    <button class="btn btn-primary" id="pw-wol-all">唤醒全部</button>' +
        '    <button class="btn" id="pw-wol-sel">唤醒选中</button>' +
        '    <span class="muted" id="pw-wol-hint">在下方勾选设备后可定向唤醒</span>' +
        '  </div>' +
        '</div>' +

        '<div class="card" style="margin-bottom:16px">' +
        '  <div class="card-head"><h3>定时电源计划</h3></div>' +
        '  <div class="power-form">' +
        '    <label>操作<select id="pw-action"><option value="shutdown">关机</option><option value="reboot">重启</option><option value="wol">唤醒</option></select></label>' +
        '    <label>时间<input type="datetime-local" id="pw-time"></label>' +
        '    <label>目标<select id="pw-target"><option value="all">全部设备</option><option value="list">选中设备</option></select></label>' +
        '    <label>备注<input type="text" id="pw-note" placeholder="可选"></label>' +
        '    <button class="btn btn-primary" id="pw-add">添加计划</button>' +
        '  </div>' +
        '  <div class="table-container" style="margin-top:12px"><table class="data-table"><thead><tr>' +
        '    <th>操作</th><th>执行时间</th><th>目标</th><th>状态</th><th>备注</th><th></th>' +
        '  </tr></thead><tbody id="pw-schedule-body"><tr><td colspan="6" class="empty-state">加载中…</td></tr></tbody></table></div>' +
        '</div>' +

        '<div class="card">' +
        '  <div class="card-head"><h3>设备</h3><input class="input-search" id="pw-search" placeholder="搜索编号/主机名/MAC"></div>' +
        '  <div class="table-container"><table class="data-table"><thead><tr>' +
        '    <th style="width:36px"><input type="checkbox" id="pw-check-all"></th>' +
        '    <th>编号</th><th>主机名</th><th>MAC</th><th>状态</th>' +
        '  </tr></thead><tbody id="pw-device-body"></tbody></table></div>' +
        '</div>';

    $("#content").html(html);

    $("#pw-wol-all").on("click", function () { doWol("all"); });
    $("#pw-wol-sel").on("click", function () { doWol("list"); });
    $("#pw-add").on("click", addSchedule);
    $("#pw-check-all").on("change", function () {
        $("#pw-device-body input[type=checkbox]").prop("checked", this.checked);
    });
    $("#pw-search").on("keyup", renderPowerDevices);

    refreshPowerDevices();
    refreshSchedules();
}

function refreshPowerDevices() {
    $.getJSON("/api/devices", function (devices) {
        _powerDevices = devices || [];
        renderPowerDevices();
    });
}

function renderPowerDevices() {
    var q = ($("#pw-search").val() || "").toLowerCase();
    var rows = _powerDevices.filter(function (d) {
        if (!q) return true;
        return String(d.assigned_id).indexOf(q) >= 0 ||
            (d.hostname || "").toLowerCase().indexOf(q) >= 0;
    }).map(function (d) {
        var pill = d.connected
            ? '<span class="pill pill-online"><i></i>在线</span>'
            : '<span class="pill pill-offline"><i></i>离线</span>';
        return '<tr><td><input type="checkbox" value="' + d.assigned_id + '"></td>' +
            '<td class="cell-id">#' + d.assigned_id + '</td>' +
            '<td class="cell-host">' + escapeHtml(d.hostname || "-") + '</td>' +
            '<td class="cell-ip">' + escapeHtml(d.mac_address || "—") + '</td>' +
            '<td>' + pill + '</td></tr>';
    }).join("");
    $("#pw-device-body").html(rows || '<tr><td colspan="5" class="empty-state">无设备</td></tr>');
}

function selectedPowerIDs() {
    return $("#pw-device-body input[type=checkbox]:checked").map(function () {
        return parseInt(this.value, 10);
    }).get();
}

function doWol(targetType) {
    var ids = targetType === "list" ? selectedPowerIDs() : [];
    if (targetType === "list" && ids.length === 0) {
        showToast("请先勾选设备", "error");
        return;
    }
    $.ajax({
        url: "/api/power/wol", method: "POST", contentType: "application/json",
        data: JSON.stringify({ target_type: targetType, device_ids: ids }),
        success: function (res) {
            showToast("已发送 " + res.sent + "/" + res.total + " 个唤醒包", res.sent > 0 ? "success" : "error");
        },
        error: function (xhr) { showToast("唤醒失败: " + errText(xhr), "error"); }
    });
}

function addSchedule() {
    var action = $("#pw-action").val();
    var runAt = $("#pw-time").val();
    var targetType = $("#pw-target").val();
    if (!runAt) { showToast("请选择执行时间", "error"); return; }
    var ids = targetType === "list" ? selectedPowerIDs() : [];
    if (targetType === "list" && ids.length === 0) { showToast("请勾选目标设备", "error"); return; }
    $.ajax({
        url: "/api/power/schedules", method: "POST", contentType: "application/json",
        data: JSON.stringify({ action: action, run_at: runAt, target_type: targetType, device_ids: ids, note: $("#pw-note").val() }),
        success: function () { showToast("已添加计划", "success"); $("#pw-note").val(""); refreshSchedules(); },
        error: function (xhr) { showToast("添加失败: " + errText(xhr), "error"); }
    });
}

function refreshSchedules() {
    $.getJSON("/api/power/schedules", function (list) {
        list = list || [];
        var actionMap = { shutdown: "关机", reboot: "重启", wol: "唤醒" };
        var statusMap = { pending: "待执行", fired: "已执行", cancelled: "已取消", failed: "失败" };
        var rows = list.map(function (s) {
            var target = s.target_type === "all" ? "全部" : (s.target_ids || []).map(function (i) { return "#" + i; }).join(" ");
            return '<tr><td>' + (actionMap[s.action] || s.action) + '</td>' +
                '<td class="mono">' + escapeHtml((s.run_at || "").replace("T", " ").slice(0, 16)) + '</td>' +
                '<td>' + escapeHtml(target) + '</td>' +
                '<td><span class="pill pill-' + s.status + '">' + (statusMap[s.status] || s.status) + '</span></td>' +
                '<td>' + escapeHtml(s.note || "") + '</td>' +
                '<td><button class="btn btn-sm btn-danger" data-id="' + s.id + '">删除</button></td></tr>';
        }).join("");
        $("#pw-schedule-body").html(rows || '<tr><td colspan="6" class="empty-state">暂无计划</td></tr>');
        $("#pw-schedule-body button[data-id]").on("click", function () {
            var id = $(this).data("id");
            $.ajax({ url: "/api/power/schedules/" + id, method: "DELETE",
                success: function () { refreshSchedules(); },
                error: function (xhr) { showToast("删除失败: " + errText(xhr), "error"); } });
        });
    });
}

function errText(xhr) {
    try { return JSON.parse(xhr.responseText).error || xhr.statusText; }
    catch (e) { return xhr.statusText || "请求失败"; }
}
