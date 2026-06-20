// 签到管理页面
function loadCheckin() {
    $.getJSON("/api/checkin/stats", function(stats) {
        renderCheckinStats(stats);
    }).fail(function() {
        $("#content").html('<div class="empty-state">无法加载签到数据</div>');
    });

    $.getJSON("/api/checkin", function(devices) {
        renderCheckinTable(devices);
    }).fail(function() {
        $("#checkin-table-container").html('<div class="empty-state">无法加载设备列表</div>');
    });
}

function renderCheckinStats(stats) {
    var html = '' +
        '<div class="stats-grid">' +
            '<div class="stat-card">' +
                '<div class="stat-value">' + stats.total + '</div>' +
                '<div class="stat-label">设备总数</div>' +
            '</div>' +
            '<div class="stat-card">' +
                '<div class="stat-value" style="color: var(--success)">' + stats.checked_in + '</div>' +
                '<div class="stat-label">已签到</div>' +
            '</div>' +
            '<div class="stat-card">' +
                '<div class="stat-value" style="color: var(--warning)">' + stats.checked_out + '</div>' +
                '<div class="stat-label">已签退</div>' +
            '</div>' +
            '<div class="stat-card">' +
                '<div class="stat-value" style="color: var(--text-secondary)">' + stats.not_checked + '</div>' +
                '<div class="stat-label">未签到</div>' +
            '</div>' +
        '</div>';
    $("#checkin-stats-container").html(html);
}

function renderCheckinTable(devices) {
    if (devices.length === 0) {
        $("#checkin-table-container").html('<div class="table-container"><div class="empty-state">暂无已注册设备</div></div>');
        return;
    }

    var rows = devices.map(function(d) {
        var statusHtml = '';
        var actionHtml = '';

        if (d.checkin_status === 0) {
            statusHtml = '<span class="badge badge-offline">未签到</span>';
            actionHtml = '<button class="btn btn-sm btn-primary" onclick="showCheckinForm(' + d.assigned_id + ')">签到</button>';
        } else if (d.checkin_status === 1) {
            statusHtml = '<span class="badge badge-online">已签到</span>';
            actionHtml = '' +
                '<button class="btn btn-sm btn-primary" style="margin-right:4px;" onclick="showSwapModal(' + d.assigned_id + ')">换设备</button>' +
                '<button class="btn btn-sm btn-primary" style="margin-right:4px; background: var(--warning);" onclick="doCheckout(' + d.assigned_id + ')">签退</button>' +
                '<button class="btn btn-sm btn-danger" onclick="doResetCheckin(' + d.assigned_id + ')">解除</button>';
        } else if (d.checkin_status === 2) {
            statusHtml = '<span class="badge badge-pending">已签退</span>';
            actionHtml = '' +
                '<button class="btn btn-sm btn-primary" style="margin-right:4px;" onclick="doRestoreCheckout(' + d.assigned_id + ')">恢复</button>' +
                '<button class="btn btn-sm btn-danger" onclick="doResetCheckin(' + d.assigned_id + ')">撤销</button>';
        }

        return '<tr>' +
            '<td><strong>#' + d.assigned_id + '</strong></td>' +
            '<td>' + escapeHtml(d.hostname) + '</td>' +
            '<td>' + escapeHtml(d.student_name || '-') + '</td>' +
            '<td>' + escapeHtml(d.student_num || '-') + '</td>' +
            '<td>' + statusHtml + '</td>' +
            '<td style="font-size:12px;">' + (d.connected ? '<span class="device-status-dot online"></span>在线' : '<span class="device-status-dot"></span>离线') + '</td>' +
            '<td>' + actionHtml + '</td>' +
            '</tr>';
    }).join("");

    var html = '' +
        '<div class="page-header">' +
            '<h2 class="section-title" style="margin:0;">设备签到列表</h2>' +
            '<div style="display:flex; gap:8px;">' +
                '<a class="btn btn-sm" style="text-decoration:none; display:inline-flex; align-items:center;" href="/api/checkin/export" download>导出 Excel</a>' +
                '<button class="btn btn-sm btn-danger" onclick="doResetAllCheckin()">解除全部签到</button>' +
            '</div>' +
        '</div>' +
        '<div class="table-container">' +
            '<table>' +
                '<thead>' +
                    '<tr>' +
                        '<th>编号</th><th>主机名</th><th>学生姓名</th><th>学号</th><th>签到状态</th><th>在线状态</th><th>操作</th>' +
                    '</tr>' +
                '</thead>' +
                '<tbody>' + rows + '</tbody>' +
            '</table>' +
        '</div>';

    $("#checkin-table-container").html(html);
}

function showCheckinForm(assignedID) {
    var html = '' +
        '<div class="modal-overlay" onclick="closeCheckinModal(event)">' +
            '<div class="modal" style="max-width:420px;" onclick="event.stopPropagation()">' +
                '<button class="modal-close" onclick="closeCheckinModal()">&times;</button>' +
                '<h2>签到 - 设备 #' + assignedID + '</h2>' +
                '<div style="margin-top:16px;">' +
                    '<div class="form-group" style="margin-bottom:12px;">' +
                        '<label style="display:block; margin-bottom:4px; font-weight:600; font-size:13px;">学生姓名</label>' +
                        '<input type="text" id="checkin-name" placeholder="请输入姓名" style="width:100%;">' +
                    '</div>' +
                    '<div class="form-group" style="margin-bottom:16px;">' +
                        '<label style="display:block; margin-bottom:4px; font-weight:600; font-size:13px;">学生学号</label>' +
                        '<input type="text" id="checkin-num" placeholder="请输入学号" style="width:100%;">' +
                    '</div>' +
                    '<button class="btn btn-primary" style="width:100%;" onclick="doCheckin(' + assignedID + ')">确认签到</button>' +
                '</div>' +
            '</div>' +
        '</div>';
    $("#checkin-modal-container").html(html);
    setTimeout(function() { $("#checkin-name").focus(); }, 100);
}

function doCheckin(assignedID) {
    var name = $("#checkin-name").val().trim();
    var num = $("#checkin-num").val().trim();
    if (!name || !num) {
        alert("请填写姓名和学号");
        return;
    }

    $.ajax({
        url: "/api/checkin/" + assignedID + "/checkin",
        method: "POST",
        contentType: "application/json",
        data: JSON.stringify({ student_name: name, student_num: num }),
        success: function() {
            closeCheckinModal();
            loadCheckin();
        },
        error: function(xhr) {
            var err = "签到失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function doCheckout(assignedID) {
    if (!confirm("确定要将设备 #" + assignedID + " 签退吗？")) return;

    $.ajax({
        url: "/api/checkin/" + assignedID + "/checkout",
        method: "POST",
        success: function() {
            loadCheckin();
        },
        error: function(xhr) {
            var err = "签退失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function doResetCheckin(assignedID) {
    if (!confirm("确定要解除设备 #" + assignedID + " 的签到吗？")) return;

    $.ajax({
        url: "/api/checkin/" + assignedID + "/reset",
        method: "POST",
        success: function() {
            loadCheckin();
        },
        error: function(xhr) {
            var err = "解除签到失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function doRestoreCheckout(assignedID) {
    if (!confirm("确定要恢复设备 #" + assignedID + " 的签到状态（撤销签退）吗？")) return;

    $.ajax({
        url: "/api/checkin/" + assignedID + "/restore",
        method: "POST",
        success: function() {
            loadCheckin();
        },
        error: function(xhr) {
            var err = "恢复签到失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function showSwapModal(fromID) {
    // Load available target devices (online and not checked in)
    $.getJSON("/api/checkin", function(devices) {
        var candidates = devices.filter(function(d) {
            return d.assigned_id !== fromID && d.checkin_status === 0 && d.connected;
        });

        var options = '';
        if (candidates.length === 0) {
            options = '<option value="">没有可用的设备</option>';
        } else {
            options = candidates.map(function(d) {
                return '<option value="' + d.assigned_id + '">#' + d.assigned_id + ' - ' + escapeHtml(d.hostname) + ' (' + escapeHtml(d.os_name || '') + ')</option>';
            }).join("");
        }

        var html = '' +
            '<div class="modal-overlay" onclick="closeCheckinModal(event)">' +
                '<div class="modal" style="max-width:420px;" onclick="event.stopPropagation()">' +
                    '<button class="modal-close" onclick="closeCheckinModal()">&times;</button>' +
                    '<h2>异常换设备 - #' + fromID + '</h2>' +
                    '<p style="margin-top:12px; color:var(--text-secondary); font-size:13px;">' +
                        '将设备 #' + fromID + ' 的签到信息迁移到新设备。请选择目标设备：' +
                    '</p>' +
                    '<div style="margin-top:16px;">' +
                        '<select id="swap-target" style="width:100%;">' + options + '</select>' +
                    '</div>' +
                    '<div style="margin-top:16px; display:flex; gap:8px;">' +
                        '<button class="btn btn-sm" style="flex:1;" onclick="closeCheckinModal()">取消</button>' +
                        '<button class="btn btn-primary btn-sm" style="flex:1;" onclick="doSwap(' + fromID + ')" ' + (candidates.length === 0 ? 'disabled' : '') + '>确认迁移</button>' +
                    '</div>' +
                '</div>' +
            '</div>';
        $("#checkin-modal-container").html(html);
    }).fail(function() {
        alert("加载设备列表失败");
    });
}

function doSwap(fromID) {
    var toID = parseInt($("#swap-target").val());
    if (!toID) {
        alert("请选择目标设备");
        return;
    }

    if (!confirm("确定要将签到信息从设备 #" + fromID + " 迁移到设备 #" + toID + " 吗？\n\n原设备将被重置为未签到状态。")) return;

    $.ajax({
        url: "/api/checkin/swap",
        method: "POST",
        contentType: "application/json",
        data: JSON.stringify({ from_assigned_id: fromID, to_assigned_id: toID }),
        success: function() {
            closeCheckinModal();
            loadCheckin();
            alert("签到信息已成功迁移到设备 #" + toID);
        },
        error: function(xhr) {
            var err = "设备迁移失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function closeCheckinModal(e) {
    if (e && e.target !== e.currentTarget) return;
    $("#checkin-modal-container").empty();
}

function doResetAllCheckin() {
    if (!confirm("确定要解除所有设备的签到状态吗？\n\n此操作不可撤销，所有已签到和已签退的设备都将被重置为未签到状态。")) return;

    $.ajax({
        url: "/api/checkin/reset-all",
        method: "POST",
        success: function(resp) {
            alert("已成功解除 " + (resp.affected_count || 0) + " 台设备的签到");
            loadCheckin();
        },
        error: function(xhr) {
            var err = "操作失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

// Render the main checkin page layout for the router
function renderCheckinPage() {
    var html = '' +
        '<h2 class="section-title" style="margin-bottom:16px;">签到管理</h2>' +
        '<div id="checkin-stats-container"></div>' +
        '<div id="checkin-table-container"></div>' +
        '<div id="checkin-modal-container"></div>';
    $("#content").html(html);
    loadCheckin();
    updateStatusBar();
}
