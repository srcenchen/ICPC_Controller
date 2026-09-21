"use strict";

var deploymentConfig = {mode: "standalone"};
var selectedRoom = "";
var fleetRooms = [];
var fleetSelection = new Set();
var fleetFilter = "";
var fleetRoomFilter = "";
var pendingPageRequests = new Set();
var roomsRequest = null;

// Cloud room mirror mode: /room/<id> makes the whole page behave as that room.
var roomPathID = (function() {
    var match = location.pathname.match(/^\/room\/([^/]+)\/?$/);
    return match ? decodeURIComponent(match[1]) : "";
})();

function isCloudRoomMirror() {
    return typeof deploymentConfig !== "undefined" && deploymentConfig.mode === "cloud" && !!selectedRoom;
}

function syncRoomURL(page) {
    if (deploymentConfig.mode !== "cloud") return;
    var target = selectedRoom ? "/room/" + encodeURIComponent(selectedRoom) : "/";
    var hash = page || currentPage || "";
    var url = target + (hash ? "#" + hash : "");
    if (location.pathname + location.hash !== url) {
        try { history.replaceState(null, "", url); } catch (e) {}
    }
}


$.ajaxPrefilter(function(options, original, request) {
    if (options.type === 'GET' && options.url !== '/api/cluster/status') {
        pendingPageRequests.add(request);
        request.always(function() { pendingPageRequests.delete(request); });
    }
    if (selectedRoom && (/^\/api\/(devices|commands|stats|checkin|network|power|presets|distribution|snapshots)(\/|\?|$)/.test(options.url) || /^\/api\/settings\/(presets|checkin)(\?|$)/.test(options.url))) {
        options.url = "/api/cluster/rooms/" + encodeURIComponent(selectedRoom) + "/proxy/" + options.url.slice(5);
    }
});

function cancelPendingPageRequests() {
    Array.from(pendingPageRequests).forEach(function(request) { request.abort(); });
    pendingPageRequests.clear();
}

$(function() {
    refreshDeployment();
    document.addEventListener('click', function(event) {
        var link = event.target.closest('a[href^="/api/"]');
        if (link && selectedRoom && /^\/api\/(devices|checkin)\/export/.test(link.getAttribute('href'))) {
            event.preventDefault();
            window.location.href = '/api/cluster/rooms/' + encodeURIComponent(selectedRoom) + '/proxy/' + link.getAttribute('href').slice(5);
        }
    });
    $("#room-scope").on("change", function() {
        if (currentPage === 'broadcast' && !bcCanLeave()) { $(this).val(selectedRoom); return; }
        selectedRoom = this.value;
        roomPathID = this.value;
        selectedTargets = [];
        allDevices = [];
        if (typeof closeTerminal === "function") closeTerminal();
        if (currentPage === 'broadcast') broadcastRoomSelection = new Set(selectedRoom ? [selectedRoom] : []);
        syncRoomURL(currentPage);
        navigateTo(currentPage);
    });
    $(document).ajaxError(function(event, xhr, options) {
        if (xhr.status !== 401 && options.type !== "GET") {
            showToast((xhr.responseJSON && xhr.responseJSON.error) || "操作失败，请检查连接后重试", "error");
        }
    });
    setInterval(function() {
        if (deploymentConfig.mode === 'cloud') refreshRoomsData();
        if (!selectedRoom) return;
        if (currentPage === "commands") loadCommandHistory();
        if (currentPage === "devices") patchDevicesFromEvent({event: "device_updated"});
        if (currentPage === "dashboard") patchDashboardStats();
    }, 5000);
});

function guardCloudPage(page) {
    if (deploymentConfig.mode !== "cloud") return page;
    if (page === "screen" && !selectedRoom) {
        showToast("请先进入一个机房再查看屏幕；云端总览不汇聚屏幕流", "info");
        return "devices";
    }
    return page;
}

function refreshDeployment() {
    $.getJSON("/api/cluster/status", function(result) {
        var previousMode = deploymentConfig.mode;
        deploymentConfig = result.deployment;
        var labels = {standalone: "单机模式", relay: "并机 · " + (deploymentConfig.room_name || "中转"), cloud: "云端模式"};
        $("#deployment-badge").text(labels[deploymentConfig.mode]);
        $("#room-scope").prop("hidden", deploymentConfig.mode !== "cloud");
        if (deploymentConfig.mode !== "cloud") {
            selectedRoom = "";
            roomPathID = "";
        } else if (roomPathID) {
            selectedRoom = roomPathID;
        }
        if (deploymentConfig.mode === "cloud") refreshRoomsData();
        var page = guardCloudPage(currentPage);
        if (page !== currentPage) navigateTo(page);
        else if (previousMode !== deploymentConfig.mode) navigateTo(page);
    });
}

function deploymentSettingsHTML(config) {
    config = config || deploymentConfig;
    return '<section class="settings-card"><h3>部署模式与机房身份</h3>' +
        '<p class="ops-intro">同一程序随时切换角色。机房主动连接云端，无需云端直连实验室。每个机房独立编号，云端以「中转身份 + 设备号」区分设备。</p>' +
        '<form id="deployment-form" class="ops-form">' +
        '<label>运行模式<select id="deploy-mode"><option value="standalone">单机 · 仅管理当前局域网</option><option value="relay">并机 · 机房中转，连接云端</option><option value="cloud">云端 · 统一管理全部机房</option></select></label>' +
        '<label>机房名称<input id="deploy-room" maxlength="100" placeholder="例如：实验楼 A301" value="' + escapeHtml(config.room_name) + '"></label>' +
        '<label>云端地址<input id="deploy-url" placeholder="https://control.example.com" value="' + escapeHtml(config.cloud_url) + '"></label>' +
        '<label>连接密钥（云端与中转一致）<input type="password" autocomplete="new-password" id="deploy-token" placeholder="' + (config.token_set ? '已配置，留空保持不变' : '至少 32 个字符') + '"></label>' +
        '<label>本机房设备号起点<input type="number" id="deploy-start" min="1" max="1000000" value="' + (config.device_id_start || 1) + '"></label>' +
        '<label>中转局域网 IP（用于 P2P）<input id="deploy-ip" placeholder="留空自动探测；多网卡建议指定" value="' + escapeHtml(config.advertise_ip) + '"></label>' +
        '<label class="ops-wide ops-check"><input id="deploy-insecure" type="checkbox" ' + (config.allow_insecure ? 'checked' : '') + '>仅可信内网允许 HTTP 明文连接（公网必须使用 HTTPS）</label>' +
        '<div class="ops-wide ops-toolbar"><button class="btn btn-primary" type="submit">保存并切换模式</button><button class="btn btn-outline" type="button" id="generate-node-key">生成随机密钥</button><span id="deployment-result" role="status"></span></div></form>' +
        '<p class="settings-desc">中转身份：<code>' + escapeHtml(config.node_id) + '</code> · 设备号起点仅影响新设备，不会重排已有编号。切换云端模式会断开本地选手机。</p></section>' +
        '<section class="settings-card"><h3>数据导出与备份</h3><p class="ops-intro">JSON 用于数据分析；ZIP 包含一致性数据库快照、广播图片与字体、SHA-256 校验清单。备份含管理员凭据与机房密钥，请妥善保管。</p>' +
        '<div class="ops-toolbar"><a class="btn btn-outline" href="/api/data/export">导出全部业务数据 · JSON</a><a class="btn btn-outline" href="/api/devices/export">设备 · Excel</a><a class="btn btn-outline" href="/api/checkin/export">签到 · Excel</a></div>' +
        '<div class="ops-toolbar"><a class="btn btn-primary" href="/api/data/backup">下载配置与数据备份</a><a class="btn btn-outline" href="/api/data/backup?uploads=true">下载完整备份（含分发文件）</a></div>' +
        '<div class="ops-note">离线恢复：先停止服务，再运行 <code>./server --db icpc.db --restore backup.zip</code>。旧数据自动保留为 .pre-restore-*，恢复成功后重新启动。恢复不含分发文件的备份时，原文件保留在旧 data 目录中。</div></section>';
}

function bindDeploymentSettings(config) {
    $("#deploy-mode").val((config || deploymentConfig).mode);
    $("#generate-node-key").on("click", function() {
        var bytes = new Uint8Array(32);
        crypto.getRandomValues(bytes);
        $("#deploy-token").attr("type", "text").val(Array.from(bytes, function(value) { return value.toString(16).padStart(2, "0"); }).join(""));
        showToast("请将相同密钥填写到云端与中转", "info");
    });
    $("#deployment-form").on("submit", function(event) {
        event.preventDefault();
        if (!confirm("切换部署配置可能断开当前连接，是否继续？")) return;
        var config = {mode: $("#deploy-mode").val(), room_name: $("#deploy-room").val(), cloud_url: $("#deploy-url").val(), token: $("#deploy-token").val(), device_id_start: Number($("#deploy-start").val()), advertise_ip: $("#deploy-ip").val().trim(), allow_insecure: $("#deploy-insecure").is(":checked")};
        $.ajax({url: "/api/settings", method: "POST", contentType: "application/json", data: JSON.stringify({deployment: config})}).done(function() {
            $("#deployment-result").text("已保存，连接将在数秒内更新");
            $("#deploy-token").val("").attr("type", "password");
            selectedRoom = "";
            refreshDeployment();
            showToast("部署配置已生效", "success");
        });
    });
}

function loadRooms() {
    if (deploymentConfig.mode !== "cloud") {
        $.getJSON("/api/cluster/status", function(result) {
            if (currentPage !== "rooms") return;
            $("#content").html('<div class="page-header"><h2>机房连接</h2><button class="btn btn-primary" onclick="navigateTo(\'settings\')">配置部署模式</button></div><div class="settings-card"><h3>' + escapeHtml(result.deployment.room_name || "当前局域网") + '</h3><p class="ops-intro">单机模式独立管理局域网设备；并机模式主动连接云端，先缓存云端文件，再在本机房 P2P 分发。</p><div class="ops-note">云端连接：' + escapeHtml(result.connection) + '</div><button class="btn btn-outline" onclick="navigateTo(\'devices\')">管理本地设备</button></div>');
        });
        return;
    }
    $("#content").html('<div class="page-header"><div><h2>机房总览</h2><p class="ops-intro">云端 → 机房中转 → 选手机。这里查看机房连接与拓扑；仪表盘、设备、命令和广播均有独立页面。</p></div><button class="btn btn-outline" onclick="refreshRoomsData()">刷新连接</button></div><div id="room-cards" class="ops-grid"></div>');
    refreshRoomsData();
}

function refreshRoomsData() {
    if (deploymentConfig.mode !== "cloud") return;
    if (roomsRequest) return roomsRequest;
    roomsRequest = $.getJSON("/api/cluster/rooms", function(rooms) {
        fleetRooms = rooms;
        if (!selectedRoom) {
            var total = 0, online = 0;
            rooms.forEach(function(room) { total += room.devices.length; online += room.devices.filter(function(device) { return device.connected; }).length; });
            $("#online-count").text("在线: " + online);
            $("#total-count").text("总计: " + total);
        }
        var options = '<option value="">全部机房</option>' + rooms.map(function(room) { return '<option value="' + escapeHtml(room.id) + '">' + escapeHtml(room.name) + (room.online ? '' : ' · 离线') + '</option>'; }).join("");
        $("#room-scope").html(options).val(selectedRoom);
        $("#fleet-room").html(options).val(fleetRoomFilter);
        $("#room-cards").html(rooms.map(function(room) {
            var online = room.devices.filter(function(device) { return device.connected; }).length;
            var task = room.distribution;
            return '<div class="settings-card room-card"><span class="badge ' + (room.online ? 'badge-online' : 'badge-offline') + '">' + (room.online ? '中转在线' : '中转离线') + '</span><h3>' + escapeHtml(room.name) + '</h3><div class="room-metric">' + online + '<small> / ' + room.devices.length + ' 台</small></div><p class="settings-desc">最后上报：' + escapeHtml(timeAgo(room.last_seen)) + '</p><p class="settings-desc">' + escapeHtml(room.transfer_phase || '') + (task ? ' · ' + statusLabel(task.status) + ' · ' + escapeHtml(task.active_file || '') : '') + '</p><button class="btn btn-outline" onclick="enterRoom(\'' + room.id + '\')">进入机房管理</button></div>';
        }).join("") || '<div class="empty-state">暂无机房连接。在机房服务器设置中选择“并机模式”，填写云端地址、密钥与机房名。</div>');
        renderFleetDevices();
        if (isCloudAllRooms()) updateCloudPage();
        if (currentPage === 'broadcast') renderBroadcastTargets();
    }).always(function() { roomsRequest = null; });
    refreshFleetJobs();
    return roomsRequest;
}

function refreshFleetJobs() {
    if (!$("#fleet-jobs").length) return;
    $.getJSON("/api/cluster/jobs", function(jobs) {
        var expanded = new Set($('#fleet-jobs details[open]').map(function() { return this.dataset.job; }).get());
        $("#fleet-jobs").html(jobs.map(function(job) {
            var room = fleetRooms.find(function(item) { return item.id === job.room_id; });
            return '<details data-job="' + escapeHtml(job.id) + '"><summary>' + escapeHtml(room ? room.name : job.room_id) + ' · ' + escapeHtml(job.created_at) + ' · ' + (job.status === 'completed' ? '已接收' : statusLabel(job.status)) + '</summary><pre class="ops-result">' + escapeHtml(job.response || '等待中转确认') + '</pre>' + (job.status === 'queued' ? '<button class="btn btn-sm btn-danger" onclick="cancelFleetJob(\'' + job.id + '\')">取消投递</button>' : '') + '</details>';
        }).join("") || '<p class="settings-desc">暂无投递记录</p>');
        $('#fleet-jobs details').each(function() { this.open = expanded.has(this.dataset.job); });
    });
}

function visibleFleetDevices() {
    var query = fleetFilter.toLowerCase();
    var devices = [];
    fleetRooms.forEach(function(room) {
        if (fleetRoomFilter && fleetRoomFilter !== room.id) return;
        room.devices.forEach(function(device) {
            var entry = Object.assign({}, device, {room_id: room.id, room_name: room.name, key: room.id + ':' + device.assigned_id});
            if ([room.name, device.assigned_id, device.hostname, device.student_name].join(' ').toLowerCase().indexOf(query) >= 0) devices.push(entry);
        });
    });
    return devices;
}

function renderFleetDevices() {
    $("#fleet-selection-count").text("已选择 " + fleetSelection.size + " 台；筛选不会自动清除其他机房的选择。");
    $("#fleet-devices").html(visibleFleetDevices().map(function(device) {
        return '<tr><td><input class="fleet-select" type="checkbox" aria-label="选择设备" value="' + escapeHtml(device.key) + '" ' + (fleetSelection.has(device.key) ? 'checked' : '') + '></td><td>' + escapeHtml(device.room_name) + ' / <strong>#' + device.assigned_id + '</strong></td><td>' + escapeHtml(device.hostname) + '<br><small>' + escapeHtml(device.os_name) + '</small></td><td>' + renderHealth(device) + '</td><td>' + escapeHtml(device.student_name || '—') + '<br><small>' + escapeHtml(device.student_num) + '</small></td><td>' + getCheckinStatusLabel(device.checkin_status) + '</td><td><span class="badge ' + (device.connected ? 'badge-online' : 'badge-offline') + '">' + (device.connected ? '在线' : '离线') + '</span></td><td><button class="btn btn-sm btn-outline fleet-manage" data-room="' + escapeHtml(device.room_id) + '" data-device="' + device.assigned_id + '">机房内管理</button></td></tr>';
    }).join("") || '<tr><td colspan="8" class="empty-state">没有匹配的设备</td></tr>');
}

function enterRoom(roomID, page) { selectedRoom = roomID; roomPathID = roomID; selectedTargets = []; allDevices = []; if (typeof closeTerminal === 'function') closeTerminal(); $("#room-scope").val(roomID); syncRoomURL(page || "devices"); navigateTo(page || "devices"); }
function openCloudFiles() { selectedRoom = ""; roomPathID = ""; $("#room-scope").val(""); syncRoomURL("distribute"); navigateTo("distribute"); }
function cancelFleetJob(id) { $.ajax({url: "/api/cluster/jobs/" + id, method: "DELETE"}).done(refreshRoomsData); }

function queueFleetOperation(operation, extra) {
    if (!fleetSelection.size) { showToast("请先选择目标设备", "error"); return; }
    var targets = {};
    fleetSelection.forEach(function(key) { var split = key.split(":"); if (!targets[split[0]]) targets[split[0]] = []; targets[split[0]].push(Number(split[1])); });
    var files = $(".fleet-file:checked").map(function() { return this.value; }).get();
    if (operation === 'command' && !((extra && extra.command) || $("#fleet-command").val() || '').trim()) { showToast("请输入命令", "error"); return; }
    if (operation === 'distribute' && !files.length) { showToast("请选择文件", "error"); return; }
    var labels = {command:'执行指令',distribute:'分发文件',network_apply:'应用网络限制',network_remove:'解除网络限制',wol:'发送唤醒包',schedule:'创建电源计划'};
    if (!confirm("将向 " + Object.keys(targets).length + " 个机房、" + fleetSelection.size + " 台设备" + labels[operation] + "。离线中转重连后会投递，确认继续？")) return;
    var body = Object.assign({operation: operation, room_ids: Object.keys(targets), targets: targets, command: $("#fleet-command").val(), files: files, save_dir: $("#fleet-save-dir").val(), post_cmd: $("#fleet-post-cmd").val()}, extra || {});
    $.ajax({url: "/api/cluster/jobs", method: "POST", contentType: "application/json", data: JSON.stringify(body)}).done(function() { showToast("任务已入队，请跟踪各机房回执", "success"); refreshFleetJobs(); });
}

function retryMissingDistribution() {
    $.ajax({url:'/api/distribution/retry',method:'POST',contentType:'application/json',data:JSON.stringify({device_id:0})}).done(function(){showToast('正在补传，已确认完成的文件将跳过','success');loadDistribute();});
}

function exportFleetCSV() {
    var rows = [["机房", "中转身份", "设备号", "主机名", "选手", "学号", "签到状态", "在线状态"]];
    fleetRooms.forEach(function(room) { room.devices.forEach(function(device) { rows.push([room.name, room.id, device.assigned_id, device.hostname, device.student_name, device.student_num, ['未签到','已签到','已签退'][device.checkin_status] || '未知', device.connected ? "在线" : "离线"]); }); });
    var csv = '\uFEFF' + rows.map(function(row) { return row.map(function(value) { var text = String(value || ''); if (/^[=+@\-\t\r]/.test(text)) text = "'" + text; return '"' + text.replace(/"/g, '""') + '"'; }).join(','); }).join('\r\n');
    var link = document.createElement('a');
    link.href = URL.createObjectURL(new Blob([csv], {type: 'text/csv;charset=utf-8'}));
    link.download = 'icpc-rooms.csv'; link.click();
    setTimeout(function() { URL.revokeObjectURL(link.href); }, 1000);
}
