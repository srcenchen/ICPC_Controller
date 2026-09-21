// 设备管理页面
function loadDevices() {
    $.getJSON("/api/devices", function(devices) {
        renderDevices(devices);
    }).fail(function() {
        if (currentPage !== "devices") return;
        $("#content").html('<div class="empty-state">无法加载设备列表</div>');
    });
}

function renderDevices(devices) {
    if (currentPage !== "devices") return;
    var rows = devices.length === 0
        ? '<tr><td colspan="11" class="empty-state">暂无已注册设备</td></tr>'
        : devices.map(function(d) {
            var checkinLabel = getCheckinStatusLabel(d.checkin_status);
            var studentInfo = d.student_name ? escapeHtml(d.student_name) + ' <small class="muted">' + escapeHtml(d.student_num) + '</small>' : '-';
            return '<tr class="clickable-row" data-device-id="' + d.assigned_id + '" onclick="showDeviceDetail(' + d.assigned_id + ')">' +
                '<td><strong>#' + d.assigned_id + '</strong></td>' +
                '<td class="col-hostname">' + escapeHtml(d.hostname) + '</td>' +
                '<td>' + escapeHtml(d.username) + '</td>' +
                '<td>' + escapeHtml(d.os_name) + '</td>' +
                '<td>' + escapeHtml(d.cpu_model) + '</td>' +
                '<td>' + renderMemBar(d.memory_used, d.memory_total) + '</td>' +
                '<td class="col-health">' + renderHealth(d) + '</td>' +
                '<td class="col-checkin">' + checkinLabel + '</td>' +
                '<td class="col-student">' + studentInfo + '</td>' +
                '<td class="col-status"><span class="badge badge-' + (d.connected ? 'online' : 'offline') + '">' + (d.connected ? '在线' : '离线') + '</span></td>' +
                '</tr>';
        }).join("");

    var html = '' +
        '<div class="page-header">' +
            '<h2 class="section-title" style="margin:0;">设备管理</h2>' +
            '<div style="display:flex; gap:8px;">' +
                '<a class="btn btn-sm btn-secondary" href="/api/devices/export" download>导出 Excel</a>' +
                '<button class="btn btn-danger btn-sm" onclick="resetAllDevices()">&#x21BA; 重置所有设备</button>' +
            '</div>' +
        '</div>' +
        '<div class="table-container">' +
            '<table>' +
                '<thead>' +
                    '<tr>' +
                        '<th>ID</th><th>主机名</th><th>用户</th><th>操作系统</th><th>CPU</th><th style="min-width:140px;">内存</th><th style="min-width:110px;">健康</th><th>签到</th><th>学生</th><th>状态</th>' +
                    '</tr>' +
                '</thead>' +
                '<tbody>' + rows + '</tbody>' +
            '</table>' +
        '</div>' +
        '<div id="device-detail-container"></div>';

    $("#content").html(html);
    updateStatusBar();
    if (typeof pendingCloudDevice !== 'undefined' && pendingCloudDevice && pendingCloudDevice.room === selectedRoom) {
        var deviceID = pendingCloudDevice.device;
        pendingCloudDevice = null;
        showDeviceDetail(deviceID);
    }
}

function showDeviceDetail(assignedID) {
    var room = selectedRoom;
    $.getJSON("/api/devices/" + assignedID, function(device) {
        if (currentPage !== 'devices' || room !== selectedRoom) return;
        renderDeviceDetail(device);
    }).fail(function() {
        alert("无法加载设备详情");
    });
}

function renderDeviceDetail(device) {
    var statusClass = device.connected ? "badge-online" : "badge-offline";
    var statusText = device.connected ? "在线" : "离线";

    var gpuInfo = "";
    try { gpuInfo = JSON.parse(device.gpu_info); } catch(e) { gpuInfo = []; }
    var gpuText = Array.isArray(gpuInfo)
        ? gpuInfo.map(function(g) { return g.vendor + " " + g.name; }).join(", ")
        : "无";

    var diskInfo = "";
    try { diskInfo = JSON.parse(device.disk_info); } catch(e) { diskInfo = []; }
    var diskText = Array.isArray(diskInfo)
        ? diskInfo.map(function(d) { return d.mountpoint + " (" + formatBytes(d.bytes.total) + ")"; }).join(", ")
        : "无";

    var ipInfo = "";
    try { ipInfo = JSON.parse(device.local_ip); } catch(e) { ipInfo = []; }
    var ipText = Array.isArray(ipInfo)
        ? ipInfo.map(function(i) { return i.name + ": " + i.ipv4; }).join(", ")
        : "无";

    var html = '' +
        '<div class="modal-overlay" onclick="closeDeviceDetail(event)">' +
            '<div class="modal" onclick="event.stopPropagation()">' +
                '<button class="modal-close" onclick="closeDeviceDetail()">&times;</button>' +
                '<h2>设备 #' + device.assigned_id + ' 详情</h2>' +
                '<span class="badge ' + statusClass + '">' + statusText + '</span>' +

                '<h3 class="modal-section">系统信息</h3>' +
                '<div class="detail-grid">' +
                    '<div class="detail-item"><div class="label">主机名</div><div class="value">' + escapeHtml(device.hostname) + '</div></div>' +
                    '<div class="detail-item"><div class="label">用户名</div><div class="value">' + escapeHtml(device.username) + '</div></div>' +
                    '<div class="detail-item"><div class="label">操作系统</div><div class="value">' + escapeHtml(device.os_pretty_name) + '</div></div>' +
                    '<div class="detail-item"><div class="label">内核</div><div class="value">' + escapeHtml(device.kernel_release) + ' (' + escapeHtml(device.kernel_arch) + ')</div></div>' +
                    '<div class="detail-item"><div class="label">Shell</div><div class="value">' + escapeHtml(device.shell) + '</div></div>' +
                    '<div class="detail-item"><div class="label">终端</div><div class="value">' + escapeHtml(device.terminal) + '</div></div>' +
                    '<div class="detail-item"><div class="label">桌面环境</div><div class="value">' + escapeHtml(device.de_name) + '</div></div>' +
                    '<div class="detail-item"><div class="label">窗口管理器</div><div class="value">' + escapeHtml(device.wm_name) + '</div></div>' +
                    '<div class="detail-item"><div class="label">运行时间</div><div class="value">' + formatUptime(device.uptime) + '</div></div>' +
                    '<div class="detail-item"><div class="label">签到状态</div><div class="value">' + getCheckinStatusLabel(device.checkin_status) + '</div></div>' +
                    '<div class="detail-item"><div class="label">学生信息</div><div class="value">' + (device.student_name ? escapeHtml(device.student_name) + ' (' + escapeHtml(device.student_num) + ')' : '-') + '</div></div>' +
                '</div>' +

                '<h3 class="modal-section">硬件信息</h3>' +
                '<div class="detail-grid">' +
                    '<div class="detail-item"><div class="label">CPU</div><div class="value">' + escapeHtml(device.cpu_model) + '</div></div>' +
                    '<div class="detail-item"><div class="label">核心数</div><div class="value">' + device.cpu_physical_cores + ' 物理 / ' + device.cpu_logical_cores + ' 逻辑</div></div>' +
                    '<div class="detail-item"><div class="label">GPU</div><div class="value">' + escapeHtml(gpuText) + '</div></div>' +
                    '<div class="detail-item"><div class="label">内存总大小</div><div class="value">' + formatBytes(device.memory_total) + '</div></div>' +
                    '<div class="detail-item"><div class="label">已用内存</div><div class="value">' + formatBytes(device.memory_used) + '</div></div>' +
                '</div>' +

                '<h3 class="modal-section">存储与网络</h3>' +
                '<div class="detail-grid">' +
                    '<div class="detail-item"><div class="label">磁盘</div><div class="value">' + escapeHtml(diskText) + '</div></div>' +
                    '<div class="detail-item"><div class="label">网络</div><div class="value">' + escapeHtml(ipText) + '</div></div>' +
                    '<div class="detail-item"><div class="label">首次上线</div><div class="value">' + formatDateTime(device.first_seen) + '</div></div>' +
                    '<div class="detail-item"><div class="label">最后在线</div><div class="value">' + formatDateTime(device.last_seen) + '</div></div>' +
                    '<div class="detail-item"><div class="label">客户端版本</div><div class="value">' + escapeHtml(device.client_version || '-') + '</div></div>' +
                '</div>' +

                '<h3 class="modal-section">健康状态</h3>' +
                '<div class="detail-grid">' +
                    '<div class="detail-item"><div class="label">CPU</div><div class="value">' + healthValue(device.cpu_pct, '%') + '</div></div>' +
                    '<div class="detail-item"><div class="label">内存</div><div class="value">' + healthValue(device.mem_pct, '%') + '</div></div>' +
                    '<div class="detail-item"><div class="label">磁盘</div><div class="value">' + healthValue(device.disk_pct, '%') + '</div></div>' +
                    '<div class="detail-item"><div class="label">温度</div><div class="value">' + healthValue(device.temp_c, '°C') + '</div></div>' +
                    '<div class="detail-item"><div class="label">负载(1m)</div><div class="value">' + (device.load1 >= 0 ? device.load1.toFixed(2) : '-') + '</div></div>' +
                    '<div class="detail-item"><div class="label">采集时间</div><div class="value">' + (device.health_at ? formatDateTime(device.health_at) : '-') + '</div></div>' +
                '</div>' +

                '<h3 class="modal-section">上下线历史</h3>' +
                '<div id="device-events-' + device.assigned_id + '" class="event-timeline"><span class="muted" style="color:var(--text-3)">加载中…</span></div>' +

                '<div style="margin-top:24px; display:flex; gap:12px;">' +
                    '<button class="btn btn-primary" onclick="selectedTargets=[' + device.assigned_id + ']; navigateTo(\'commands\');">' +
                        '在此设备执行命令' +
                    '</button>' +
                    '<button class="btn btn-sm btn-accent" onclick="openTerminal(' + device.assigned_id + ')">🖥 终端</button>' +
                    '<button class="btn btn-danger btn-sm" onclick="deleteDevice(' + device.assigned_id + ')">移除设备</button>' +
                '</div>' +
            '</div>' +
        '</div>';

    $("#device-detail-container").html(html);
    loadDeviceEvents(device.assigned_id);
}

function healthValue(v, unit) {
    if (v === undefined || v === null || v < 0) return '<span style="color:var(--text-3)">未采集</span>';
    return v.toFixed(unit === '°C' ? 0 : 1) + unit;
}

function loadDeviceEvents(assignedID) {
    $.getJSON("/api/devices/" + assignedID + "/events", function(events) {
        var el = $("#device-events-" + assignedID);
        if (!el.length) return;
        if (!events || events.length === 0) {
            el.html('<span class="muted" style="color:var(--text-3)">暂无记录</span>');
            return;
        }
        var map = { online: '上线', offline: '下线', alert: '告警', registered: '注册' };
        var rows = events.slice(0, 40).map(function(e) {
            var color = e.event === 'online' ? 'var(--success)' : (e.event === 'alert' ? 'var(--danger)' : 'var(--text-3)');
            return '<div class="event-row"><span class="event-dot" style="background:' + color + '"></span>' +
                '<span class="event-label">' + (map[e.event] || e.event) + '</span>' +
                (e.detail ? '<span class="event-detail">' + escapeHtml(e.detail) + '</span>' : '') +
                '<span class="event-time" title="' + escapeHtml(e.at) + '">' + timeAgo(e.at) + '</span></div>';
        }).join("");
        el.html(rows);
    }).fail(function() {
        $("#device-events-" + assignedID).html('<span class="muted" style="color:var(--text-3)">加载失败</span>');
    });
}

function closeDeviceDetail(e) {
    if (e && e.target !== e.currentTarget) return;
    $("#device-detail-container").empty();
}

function deleteDevice(assignedID) {
    if (!confirm("确定要移除设备 #" + assignedID + " 吗？")) return;

    $.ajax({
        url: "/api/devices/" + assignedID,
        method: "DELETE",
        success: function() {
            $("#device-detail-container").empty();
            loadDevices();
            updateStatusBar();
        },
        error: function() { alert("删除设备失败"); }
    });
}

// renderHealth renders CPU/mem/temp mini-indicators from heartbeat metrics.
// -1 means the client hasn't reported (or the sensor is unavailable).
function renderHealth(d) {
    if (!d.connected) return '<span class="muted" style="color:var(--text-3)">—</span>';
    if (d.health_at === "" || d.health_at === undefined) {
        return '<span class="muted" style="color:var(--text-3)">等待…</span>';
    }
    function bar(val, warn, crit, label) {
        if (val === undefined || val < 0) return '';
        var cls = val >= crit ? 'crit' : (val >= warn ? 'warn' : '');
        var w = Math.max(0, Math.min(100, val));
        return '<span class="health" title="' + label + ' ' + val.toFixed(0) + '%">' +
            '<span class="health-bar ' + cls + '"><i style="width:' + w + '%"></i></span></span>';
    }
    var out = bar(d.cpu_pct, 75, 90, 'CPU') + bar(d.mem_pct, 80, 92, '内存') + bar(d.disk_pct, 80, 90, '磁盘');
    if (d.temp_c !== undefined && d.temp_c >= 0) {
        var tc = d.temp_c >= 85 ? 'var(--danger)' : (d.temp_c >= 70 ? 'var(--warning)' : 'var(--text-2)');
        out += ' <span style="font-size:11px;color:' + tc + '">' + d.temp_c.toFixed(0) + '°C</span>';
    }
    return out || '<span class="muted" style="color:var(--text-3)">—</span>';
}

function renderMemBar(used, total) {
    if (!total || total <= 0) return formatBytes(total || 0);
    var pct = Math.round(used / total * 100);
    var cls = pct > 90 ? 'critical' : (pct > 70 ? 'high' : '');
    return '<div class="mem-bar">' +
        '<div class="mem-bar-fill ' + cls + '" style="width:' + pct + '%"></div>' +
        '<div class="mem-bar-text">' + formatBytes(used) + ' / ' + formatBytes(total) + '</div>' +
        '</div>';
}

function getCheckinStatusLabel(status) {
    if (status === 1) return '<span class="badge badge-online">已签到</span>';
    if (status === 2) return '<span class="badge badge-pending">已签退</span>';
    return '<span class="badge badge-offline">未签到</span>';
}

function resetAllDevices() {
    if (!confirm("这将删除所有设备记录并断开所有客户端连接。\n客户端将以新 ID 重新连接，从 1 开始。\n\n确定要重置吗？")) return;

    $.ajax({
        url: "/api/devices/reset",
        method: "POST",
        success: function() {
            $("#device-detail-container").empty();
            loadDevices();
            updateStatusBar();
            alert("所有设备已重置。客户端将用新 ID 重新连接。");
        },
        error: function(xhr) {
            var err = "重置失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function formatUptime(seconds) {
    if (!seconds || seconds <= 0) return "0分";
    var totalMinutes = Math.floor(seconds / 60);
    var hours = Math.floor(totalMinutes / 60);
    var minutes = totalMinutes % 60;
    
    if (hours > 0) {
        return hours + "时 " + minutes + "分";
    }
    return minutes + "分";
}

function formatDateTime(str) {
    if (!str) return "-";
    try {
        var d = new Date(str);
        if (isNaN(d.getTime())) return str;
        var pad = function(n) { return n < 10 ? '0' + n : String(n); };
        return d.getFullYear() + '-' + pad(d.getMonth()+1) + '-' + pad(d.getDate()) + ' ' +
               pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    } catch(e) {
        return str;
    }
}

// patchDeviceHealth updates a single row's health cell from a live report.
function patchDeviceHealth(data) {
    if (!data || !data.assigned_id) return;
    var row = $('tr[data-device-id="' + data.assigned_id + '"]');
    if (!row.length) return;
    // The event carries current metrics; merge with connected=true for rendering.
    var d = {
        connected: true, health_at: "live",
        cpu_pct: data.cpu_pct, mem_pct: data.mem_pct, disk_pct: data.disk_pct, temp_c: data.temp_c
    };
    row.find(".col-health").html(renderHealth(d));
    if (data.alert) {
        row.find(".col-health").attr("title", "告警: " + data.alert);
    }
}

// Partial update for WS events — avoid full table rebuild when possible.
function patchDevicesFromEvent(msg) {
    if (currentPage !== "devices") return;
    if (!$("#content .table-container tbody").length) {
        loadDevices();
        return;
    }
    var id = msg && msg.data && msg.data.assigned_id;
    if (!id) {
        // Soft refresh list without wiping open detail modal.
        $.getJSON("/api/devices", function(devices) {
            devices.forEach(function(d) {
                var row = $('tr[data-device-id="' + d.assigned_id + '"]');
                if (!row.length) return;
                row.find(".col-hostname").text(d.hostname || "");
                row.find(".col-checkin").html(getCheckinStatusLabel(d.checkin_status));
                var studentInfo = d.student_name ? escapeHtml(d.student_name) + ' <small class="muted">' + escapeHtml(d.student_num) + '</small>' : '-';
                row.find(".col-student").html(studentInfo);
                row.find(".col-status").html('<span class="badge badge-' + (d.connected ? 'online' : 'offline') + '">' + (d.connected ? '在线' : '离线') + '</span>');
            });
        });
        return;
    }
    $.getJSON("/api/devices/" + id, function(d) {
        var row = $('tr[data-device-id="' + d.assigned_id + '"]');
        if (!row.length) {
            loadDevices();
            return;
        }
        row.find(".col-hostname").text(d.hostname || "");
        row.find(".col-checkin").html(getCheckinStatusLabel(d.checkin_status));
        var studentInfo = d.student_name ? escapeHtml(d.student_name) + ' <small class="muted">' + escapeHtml(d.student_num) + '</small>' : '-';
        row.find(".col-student").html(studentInfo);
        row.find(".col-status").html('<span class="badge badge-' + (d.connected ? 'online' : 'offline') + '">' + (d.connected ? '在线' : '离线') + '</span>');
    }).fail(function() {
        // ignore single-row fail
    });
}
