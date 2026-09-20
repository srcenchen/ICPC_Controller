// 仪表盘页面
function loadDashboard() {
    $.when(
        $.getJSON("/api/stats"),
        $.getJSON("/api/distribution/status").then(null, function() { return $.Deferred().resolve([null]).promise(); }),
        $.getJSON("/api/checkin/stats").then(null, function() { return $.Deferred().resolve([{}]).promise(); })
    ).done(function(statsRes, distRes, checkinRes) {
        var stats = statsRes[0] || statsRes;
        var dist = distRes && distRes[0] ? distRes[0] : null;
        var checkin = checkinRes && checkinRes[0] ? checkinRes[0] : {};
        renderDashboard(stats, dist, checkin);
    }).fail(function() {
        $("#content").html('<div class="empty-state">无法加载仪表盘数据 — 请检查服务端是否运行</div>');
    });
}

function patchDashboardStats() {
    if (currentPage !== "dashboard") return;
    if (!$("#dash-online").length) {
        loadDashboard();
        return;
    }
    $.getJSON("/api/stats", function(stats) {
        $("#dash-total").text(stats.total_devices);
        $("#dash-online").text(stats.online_devices);
        $("#dash-offline").text(stats.offline_devices);
        $("#dash-checkedin").text(stats.checked_in || 0);
        $("#dash-commands").text(stats.total_commands);
        if (stats.recent_commands) {
            $("#dash-recent-commands tbody").html(renderCommandRows(stats.recent_commands));
        }
    });
    patchDashboardTasks();
}

function patchDashboardTasks() {
    if (currentPage !== "dashboard" || !$("#dash-dist-panel").length) return;
    $.getJSON("/api/distribution/status").done(function(dist) {
        $("#dash-dist-panel").html(renderDistSummary(dist));
    }).fail(function() {
        $("#dash-dist-panel").html('<div class="empty-state" style="padding:12px;">暂无分发任务</div>');
    });
}

function renderDistSummary(dist) {
    if (!dist || !dist.task_id && !dist.status) {
        // status API may return full task object
        if (!dist || dist.status === undefined) {
            return '<div class="empty-state" style="padding:12px;">暂无进行中的分发</div>';
        }
    }
    var status = dist.status || "unknown";
    var file = dist.active_file || (dist.files && dist.files[dist.active_idx]) || "-";
    var progresses = dist.progresses || {};
    var total = 0, completed = 0, failed = 0;
    var sumPct = 0;
    Object.keys(progresses).forEach(function(k) {
        var p = progresses[k];
        total++;
        sumPct += (p.percentage || 0);
        if (p.status === "completed") completed++;
        if (p.status === "failed" || p.status === "stalled" || p.status === "cancelled") failed++;
    });
    if (dist.total !== undefined) total = dist.total;
    if (dist.completed !== undefined) completed = dist.completed;
    if (dist.failed !== undefined) failed = dist.failed;
    var avg = total > 0 ? (sumPct / total) : (dist.avg_pct || 0);
    return '' +
        '<div style="display:flex;flex-wrap:wrap;gap:12px;align-items:center;">' +
            '<span class="badge badge-' + escapeHtml(status) + '">' + escapeHtml(status) + '</span>' +
            '<span>文件: <code>' + escapeHtml(String(file)) + '</code></span>' +
            '<span>进度: ' + avg.toFixed(1) + '%</span>' +
            '<span>完成 ' + completed + ' / 失败 ' + failed + ' / 共 ' + total + '</span>' +
            '<a href="#distribute" class="btn btn-sm" onclick="navigateTo(\'distribute\');return false;">查看分发</a>' +
        '</div>';
}

function renderDashboard(stats, dist, checkin) {
    var unchecked = checkin.not_checked != null ? checkin.not_checked :
        Math.max(0, (stats.total_devices || 0) - (stats.checked_in || 0));
    var html = '' +
        '<div class="stats-grid">' +
            '<div class="stat-card" style="cursor:pointer" onclick="navigateTo(\'devices\')">' +
                '<div class="stat-value" id="dash-total">' + stats.total_devices + '</div>' +
                '<div class="stat-label">设备总数</div>' +
            '</div>' +
            '<div class="stat-card" style="cursor:pointer" onclick="navigateTo(\'devices\')">' +
                '<div class="stat-value" id="dash-online" style="color: var(--success)">' + stats.online_devices + '</div>' +
                '<div class="stat-label">在线设备</div>' +
            '</div>' +
            '<div class="stat-card" style="cursor:pointer" onclick="navigateTo(\'devices\')">' +
                '<div class="stat-value" id="dash-offline" style="color: var(--danger)">' + stats.offline_devices + '</div>' +
                '<div class="stat-label">离线设备</div>' +
            '</div>' +
            '<div class="stat-card" style="cursor:pointer" onclick="navigateTo(\'checkin\')">' +
                '<div class="stat-value" id="dash-checkedin" style="color: var(--success)">' + (stats.checked_in || 0) + '</div>' +
                '<div class="stat-label">已签到</div>' +
            '</div>' +
            '<div class="stat-card" style="cursor:pointer" onclick="navigateTo(\'checkin\')">' +
                '<div class="stat-value">' + unchecked + '</div>' +
                '<div class="stat-label">未签到</div>' +
            '</div>' +
            '<div class="stat-card" style="cursor:pointer" onclick="navigateTo(\'commands\')">' +
                '<div class="stat-value" id="dash-commands">' + stats.total_commands + '</div>' +
                '<div class="stat-label">命令总数</div>' +
            '</div>' +
        '</div>' +

        '<div class="card" style="margin-bottom:16px;">' +
            '<h2 class="section-title" style="margin-top:0;">进行中任务</h2>' +
            '<div id="dash-dist-panel">' + renderDistSummary(dist) + '</div>' +
            '<div style="margin-top:12px;display:flex;gap:8px;flex-wrap:wrap;">' +
                '<a class="btn btn-sm" href="#broadcast" onclick="navigateTo(\'broadcast\');return false;">广播管理</a>' +
                '<a class="btn btn-sm" href="#network" onclick="navigateTo(\'network\');return false;">网络屏蔽</a>' +
                '<a class="btn btn-sm" href="#screen" onclick="navigateTo(\'screen\');return false;">选手屏幕</a>' +
            '</div>' +
        '</div>' +

        '<h2 class="section-title">最近命令</h2>' +
        '<div class="table-container" id="dash-recent-commands">' +
            '<table>' +
                '<thead>' +
                    '<tr>' +
                        '<th>ID</th><th>时间</th><th>目标</th><th>命令</th><th>状态</th><th>耗时</th>' +
                    '</tr>' +
                '</thead>' +
                '<tbody>' + renderCommandRows(stats.recent_commands) + '</tbody>' +
            '</table>' +
        '</div>';

    $("#content").html(html);
    updateStatusBar();
}

function renderCommandRows(commands) {
    if (!commands || commands.length === 0) {
        return '<tr><td colspan="6" class="empty-state">暂无命令记录</td></tr>';
    }
    return commands.map(function(cmd) {
        var target = cmd.target_type === "broadcast" ? "全部" : ("#" + cmd.target_id);
        return '<tr>' +
            '<td>#' + cmd.id + '</td>' +
            '<td>' + cmd.created_at + '</td>' +
            '<td>' + target + '</td>' +
            '<td><code>' + escapeHtml(cmd.command.substring(0, 60)) + (cmd.command.length > 60 ? '...' : '') + '</code></td>' +
            '<td><span class="badge badge-' + escapeHtml(cmd.status) + '">' + statusLabel(cmd.status) + '</span></td>' +
            '<td>' + (cmd.duration_ms > 0 ? (cmd.duration_ms + 'ms') : '-') + '</td>' +
        '</tr>';
    }).join("");
}
