// 设备管理页面
function loadDevices() {
    $.getJSON("/api/devices", function(devices) {
        renderDevices(devices);
    }).fail(function() {
        $("#content").html('<div class="empty-state">无法加载设备列表</div>');
    });
}

function renderDevices(devices) {
    var rows = devices.length === 0
        ? '<tr><td colspan="10" class="empty-state">暂无已注册设备</td></tr>'
        : devices.map(function(d) {
            var checkinLabel = getCheckinStatusLabel(d.checkin_status);
            var studentInfo = d.student_name ? escapeHtml(d.student_name) + ' <small style="color:var(--text-secondary)">' + escapeHtml(d.student_num) + '</small>' : '-';
            return '<tr class="clickable-row" onclick="showDeviceDetail(' + d.assigned_id + ')">' +
                '<td><strong>#' + d.assigned_id + '</strong></td>' +
                '<td>' + escapeHtml(d.hostname) + '</td>' +
                '<td>' + escapeHtml(d.username) + '</td>' +
                '<td>' + escapeHtml(d.os_name) + '</td>' +
                '<td>' + escapeHtml(d.cpu_model) + '</td>' +
                '<td>' + renderMemBar(d.memory_used, d.memory_total) + '</td>' +
                '<td>' + checkinLabel + '</td>' +
                '<td>' + studentInfo + '</td>' +
                '<td><span class="badge badge-' + (d.connected ? 'online' : 'offline') + '">' + (d.connected ? '在线' : '离线') + '</span></td>' +
                '</tr>';
        }).join("");

    var html = '' +
        '<div class="page-header">' +
            '<h2 class="section-title" style="margin:0;">设备管理</h2>' +
            '<div style="display:flex; gap:8px;">' +
                '<a class="btn btn-sm" style="text-decoration:none; display:inline-flex; align-items:center;" href="/api/devices/export" download>导出 Excel</a>' +
                '<button class="btn btn-danger btn-sm" onclick="resetAllDevices()">&#x21BA; 重置所有设备</button>' +
            '</div>' +
        '</div>' +
        '<div class="table-container">' +
            '<table>' +
                '<thead>' +
                    '<tr>' +
                        '<th>ID</th><th>主机名</th><th>用户</th><th>操作系统</th><th>CPU</th><th style="min-width:140px;">内存</th><th>签到</th><th>学生</th><th>状态</th>' +
                    '</tr>' +
                '</thead>' +
                '<tbody>' + rows + '</tbody>' +
            '</table>' +
        '</div>' +
        '<div id="device-detail-container"></div>';

    $("#content").html(html);
    updateStatusBar();
}

function showDeviceDetail(assignedID) {
    $.getJSON("/api/devices/" + assignedID, function(device) {
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

                '<h3 style="margin-top:20px; color:var(--accent)">系统信息</h3>' +
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

                '<h3 style="margin-top:20px; color:var(--accent)">硬件信息</h3>' +
                '<div class="detail-grid">' +
                    '<div class="detail-item"><div class="label">CPU</div><div class="value">' + escapeHtml(device.cpu_model) + '</div></div>' +
                    '<div class="detail-item"><div class="label">核心数</div><div class="value">' + device.cpu_physical_cores + ' 物理 / ' + device.cpu_logical_cores + ' 逻辑</div></div>' +
                    '<div class="detail-item"><div class="label">GPU</div><div class="value">' + escapeHtml(gpuText) + '</div></div>' +
                    '<div class="detail-item"><div class="label">内存总大小</div><div class="value">' + formatBytes(device.memory_total) + '</div></div>' +
                    '<div class="detail-item"><div class="label">已用内存</div><div class="value">' + formatBytes(device.memory_used) + '</div></div>' +
                '</div>' +

                '<h3 style="margin-top:20px; color:var(--accent)">存储与网络</h3>' +
                '<div class="detail-grid">' +
                    '<div class="detail-item"><div class="label">磁盘</div><div class="value">' + escapeHtml(diskText) + '</div></div>' +
                    '<div class="detail-item"><div class="label">网络</div><div class="value">' + escapeHtml(ipText) + '</div></div>' +
                    '<div class="detail-item"><div class="label">首次上线</div><div class="value">' + formatDateTime(device.first_seen) + '</div></div>' +
                    '<div class="detail-item"><div class="label">最后在线</div><div class="value">' + formatDateTime(device.last_seen) + '</div></div>' +
                '</div>' +

                '<div style="margin-top:24px; display:flex; gap:12px;">' +
                    '<button class="btn btn-primary" onclick="selectedTargets=[' + device.assigned_id + ']; navigateTo(\'commands\');">' +
                        '在此设备执行命令' +
                    '</button>' +
                    '<button class="btn btn-sm" style="background:var(--accent);color:#fff;" onclick="openTerminal(' + device.assigned_id + ')">🖥 终端</button>' +
                    '<button class="btn btn-danger btn-sm" onclick="deleteDevice(' + device.assigned_id + ')">移除设备</button>' +
                '</div>' +
            '</div>' +
        '</div>';

    $("#device-detail-container").html(html);
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
        var pad = function(n) { return n < 0 ? '0' : (n < 10 ? '0' + n : n); };
        return d.getFullYear() + '-' + pad(d.getMonth()+1) + '-' + pad(d.getDate()) + ' ' +
               pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
    } catch(e) {
        return str;
    }
}
