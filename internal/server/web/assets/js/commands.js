// Commands page — redesigned for 50–100 devices
"use strict";

var cmdEditor = null;
var presetCommands = [];
var activeBatch = null;     // { cmdIds: {}, rows: {deviceKey: {el, status, cmdId, deviceId, summary}} }
var activeRowKey = null;
var sessionTotal = 0;
var sessionCompleted = 0;
var watchingCmdId = null;
var watchPollTimer = null;
var staticDetailData = {};  // { deviceId: fullOutput } for history static output rows

function loadCommands() {
    if (watchPollTimer) { clearInterval(watchPollTimer); watchPollTimer = null; }
    watchingCmdId = null;
    activeBatch = null;
    activeRowKey = null;
    sessionTotal = 0;
    sessionCompleted = 0;
    deviceFilter = "";

    $.getJSON("/api/devices", function(devices) {
        allDevices = devices;
        renderCommandPage(devices);
    }).fail(function() {
        $("#content").html('<div class="empty-state">无法加载设备列表</div>');
    });
}

// ---- Page render ----

function renderCommandPage(devices) {
    allDevices = devices;
    var html = '' +
    '<h2 class="section-title">命令执行</h2>' +
    '<div class="command-layout">' +
        '<div class="command-panel">' +
            '<div class="panel-title">目标设备</div>' +
            '<div id="device-selector-container"></div>' +
            '<div class="panel-title" style="margin-top:16px;">命令</div>' +
            '<div id="preset-buttons" style="margin-bottom:8px; display:flex; flex-wrap:wrap; gap:6px;">加载预设...</div>' +
            '<textarea id="command-editor">echo "Hello from ICPC!"</textarea>' +
            '<div style="margin-top:12px;">' +
                '<button class="btn btn-primary" onclick="executeCommand()">▶ 执行</button>' +
            '</div>' +
        '</div>' +
        '<div class="result-panel" style="display:flex; flex-direction:column;">' +
            '<div class="result-header">' +
                '<div class="panel-title">执行结果</div>' +
                '<span id="result-progress" style="font-size:12px; color:var(--text-secondary);"></span>' +
            '</div>' +
            '<div id="result-area" style="flex:1; min-height:0; display:flex; flex-direction:column;">' +
                '<div class="empty-state">选择目标设备并点击执行，或选择历史命令查看结果</div>' +
            '</div>' +
        '</div>' +
    '</div>' +

    '<div class="page-header" style="margin-top:24px;">' +
        '<h2 class="section-title" style="margin:0;">命令历史</h2>' +
        '<button class="btn btn-danger btn-sm" onclick="clearCommandHistory()">清空历史</button>' +
    '</div>' +
    '<div class="table-container">' +
        '<table>' +
            '<thead><tr><th>ID</th><th>时间</th><th>目标</th><th>命令</th><th>状态</th><th>耗时</th></tr></thead>' +
            '<tbody id="history-tbody"><tr><td colspan="6" class="empty-state">加载中...</td></tr></tbody>' +
        '</table>' +
    '</div>';

    $("#content").html(html);
    renderDeviceList();
    setTimeout(initCodeMirror, 100);
    loadPresets();
    loadCommandHistory();
}

function initCodeMirror() {
    // Destroy previous instance to avoid duplicates on page re-render.
    if (cmdEditor) {
        var prevEl = cmdEditor.getWrapperElement();
        if (prevEl && prevEl.parentNode) cmdEditor.toTextArea();
        cmdEditor = null;
    }

    var ta = document.getElementById("command-editor");
    if (!ta) return;

    // Guard against CodeMirror already having wrapped this textarea.
    if (ta.classList.contains("CodeMirror")) return;
    if (ta.nextSibling && ta.nextSibling.classList && ta.nextSibling.classList.contains("CodeMirror")) return;

    cmdEditor = CodeMirror.fromTextArea(ta, {
        mode: "shell", theme: "eclipse", lineNumbers: true,
        lineWrapping: true, indentUnit: 2, tabSize: 2,
        matchBrackets: true, autoCloseBrackets: true
    });
    cmdEditor.setSize(null, "200px");
}

// ---- Presets ----
function loadPresets() {
    $.getJSON("/api/presets", function(presets) {
        presetCommands = presets;
        var html = '';
        presets.forEach(function(p, i) {
            html += '<button class="btn btn-sm btn-' + (p.color || 'primary') + '" title="' + escapeHtml(p.desc) + '" onclick="applyPreset(' + i + ')">' + escapeHtml(p.name) + '</button>';
        });
        $("#preset-buttons").html(html);
    }).fail(function() { $("#preset-buttons").html(''); });
}
function applyPreset(idx) {
    var cmd = presetCommands[idx] ? presetCommands[idx].command : "";
    if (!cmd) return;
    if (cmdEditor) cmdEditor.setValue(cmd); else $("#command-editor").val(cmd);
}

// ---- Execute ----
function executeCommand() {
    var command = cmdEditor ? cmdEditor.getValue() : $("#command-editor").val();
    if (!command.trim()) { alert("请输入命令"); return; }
    if (selectedTargets.length === 0 && allDevices.length === 0) { alert("没有可用设备"); return; }

    activeBatch = { cmdIds: {}, rows: {} };
    activeRowKey = null;
    sessionCompleted = 0;
    $("#result-progress").text('');

    var commandText = command.trim();

    if (selectedTargets.length === 0) {
        // Broadcast
        var onlineCount = allDevices.filter(function(d) { return d.connected; }).length;
        sessionTotal = onlineCount > 0 ? onlineCount : allDevices.length;
        $("#result-area").html(renderResultTablePlaceholder());
        $.ajax({
            url: "/api/commands", method: "POST", contentType: "application/json",
            data: JSON.stringify({ target_type: "broadcast", command: commandText }),
            success: function(cmd) {
                activeBatch.cmdIds[cmd.id] = true;
                updateResultTableCaption("已派发广播 #" + cmd.id + "，等待设备响应...", "");
                loadCommandHistory();
            },
            error: function(xhr) { showExecError(xhr); }
        });
    } else {
        sessionTotal = selectedTargets.length;
        $("#result-area").html(renderResultTablePlaceholder());
        for (var i = 0; i < selectedTargets.length; i++) {
            (function(deviceId) {
                $.ajax({
                    url: "/api/commands", method: "POST", contentType: "application/json",
                    data: JSON.stringify({ target_type: "single", target_id: deviceId, command: commandText }),
                    success: function(cmd) {
                        activeBatch.cmdIds[cmd.id] = true;
                        updateResultTableCaption("已派发 " + selectedTargets.length + " 个命令，等待响应...", "");
                        loadCommandHistory();
                    },
                    error: function(xhr) { showExecError(xhr); }
                });
            })(selectedTargets[i]);
        }
    }
}

function showExecError(xhr) {
    var err = "执行失败";
    try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
    $("#result-area").html('<div class="command-output" style="color:var(--danger)">' + escapeHtml(err) + '</div>');
}

// ---- Result table ----

function renderResultTablePlaceholder() {
    return '<div class="result-summary" style="display:flex;flex-direction:column;flex:1;min-height:0;">' +
        '<div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px;">' +
            '<span id="result-table-caption" style="font-size:13px;color:var(--text-secondary);">等待设备响应...</span>' +
            '<span id="result-table-progress" style="font-size:12px;color:var(--text-secondary);"></span>' +
        '</div>' +
        '<div style="flex:1;overflow-y:auto;">' +
            '<table class="result-table">' +
                '<thead><tr><th class="col-device">设备</th><th class="col-status">状态</th><th class="col-summary">输出摘要</th><th class="col-duration">耗时</th></tr></thead>' +
                '<tbody id="result-tbody"></tbody>' +
            '</table>' +
        '</div>' +
        '<div id="result-detail" style="display:none; margin-top:8px;"></div>' +
    '</div>';
}

function updateResultTableCaption(caption, progress) {
    $("#result-table-caption").text(caption);
    $("#result-table-progress").text(progress);
}

function getOrCreateRow(deviceId, cmdId) {
    var key = "dev_" + deviceId;
    if (!activeBatch.rows[key]) {
        var row = $('<tr class="result-row running" data-key="' + key + '">' +
            '<td class="col-device"><strong>#' + deviceId + '</strong></td>' +
            '<td class="col-status"><span class="badge badge-running">运行中</span></td>' +
            '<td class="col-summary"><span style="color:var(--text-secondary)">等待输出...</span></td>' +
            '<td class="col-duration">-</td>' +
        '</tr>');
        row.on("click", function() { showCommandResultDetail(key); });
        activeBatch.rows[key] = { el: row, cmdId: cmdId, status: "running", deviceId: deviceId, summary: "" };
        $("#result-tbody").append(row);
    }
    return activeBatch.rows[key];
}

function showCommandResultDetail(key) {
    if (!activeBatch || !activeBatch.rows) return;
    // Toggle: clicking the same row closes the detail.
    if (activeRowKey === key) {
        closeCommandResultDetail();
        return;
    }
    activeRowKey = key;
    $(".result-row").removeClass("active");
    var row = activeBatch.rows[key];
    if (!row) return;
    row.el.addClass("active");

    var detail = $("#result-detail");
    detail.show();
    detail.html(
        '<div class="result-detail-header">' +
            '<span class="result-detail-device">设备 #' + row.deviceId + ' 输出详情</span>' +
            '<div style="display:flex;align-items:center;gap:8px;">' +
                '<span style="font-size:11px;color:var(--text-secondary);">单击行展开 / 再次单击关闭</span>' +
                (row.status === "running" ? '<button class="btn btn-danger btn-sm" onclick="cancelDeviceCommand(\'' + key + '\')">⏹ 终止</button>' : '') +
                '<button class="btn btn-sm" onclick="closeCommandResultDetail()">✕</button>' +
            '</div>' +
        '</div>' +
        '<div class="command-output" style="max-height:200px;">' + (row.summary ? escapeHtml(row.summary) : '<span style="color:var(--text-secondary)">(等待输出)</span>') + '</div>'
    );
}

function updateRowStatus(key, status, durationMs) {
    var row = activeBatch.rows[key];
    if (!row) return;
    row.status = status;
    var label = statusLabel(status);
    var badgeClass = "badge-" + status;
    if (status === "running") badgeClass = "badge-running";
    row.el.find(".col-status").html('<span class="badge ' + badgeClass + '">' + label + '</span>');
    row.el.find(".col-duration").text(durationMs ? (durationMs + 'ms') : '-');
    row.el.removeClass("running failed completed").addClass(status);
}

function updateRowSummary(key, text) {
    var row = activeBatch.rows[key];
    if (!row) return;
    row.summary = text;
    var short = text.replace(/\n/g, ' ').substring(0, 80);
    if (text.length > 80) short += '...';
    row.el.find(".col-summary").html('<span style="font-size:12px;">' + escapeHtml(short) + '</span>');
}

function updateProgress() {
    if (sessionTotal <= 0) { updateResultTableCaption("等待设备响应...", ""); return; }
    var completed = 0;
    for (var k in activeBatch.rows) {
        if (activeBatch.rows[k].status !== "running") completed++;
    }
    var allDone = completed >= sessionTotal;
    var failCount = 0;
    for (var k2 in activeBatch.rows) {
        if (activeBatch.rows[k2].status === "failed" || activeBatch.rows[k2].status === "timeout") failCount++;
    }
    var prog = completed + '/' + sessionTotal;
    if (failCount > 0) prog += ' · 失败 ' + failCount;
    if (allDone) prog += ' ✓';
    updateResultTableCaption("", prog);
}

function closeCommandResultDetail() {
    activeRowKey = null;
    var detail = $("#result-detail");
    if (detail.length) detail.hide();
    $(".result-row").removeClass("active");
}

function cancelDeviceCommand(key) {
    if (!activeBatch || !activeBatch.rows || !activeBatch.rows[key]) return;
    $.ajax({ url: "/api/commands/" + activeBatch.rows[key].cmdId + "/cancel", method: "POST" });
}

// ---- Real-time streaming ----

function isActiveEvent(evt) {
    if (!activeBatch) return false;
    if (activeBatch.cmdIds[evt.command_id]) return true;
    if (evt.command_id && evt.device_id) {
        activeBatch.cmdIds[evt.command_id] = true;
        return true;
    }
    return false;
}

function handleCommandOutput(evt) {
    if (isActiveEvent(evt)) {
        var key = "dev_" + evt.device_id;
        var row = getOrCreateRow(evt.device_id, evt.command_id);
        row.summary += evt.line + '\n';
        updateRowSummary(key, row.summary);
        // Update detail view if visible
        if (activeRowKey === key) {
            $("#result-detail .command-output").text(row.summary);
            var detailEl = $("#result-detail .command-output")[0];
            if (detailEl) detailEl.scrollTop = detailEl.scrollHeight;
        }
        return;
    }
    // Watching from history
    if (watchingCmdId && (evt.command_id === watchingCmdId || evt.device_id)) {
        var el = $("#result-detail .command-output");
        if (el.length > 0) {
            var color = evt.stream === "stderr" ? "var(--danger)" : "var(--success)";
            el.append('<span style="color:' + color + '">' + escapeHtml(evt.line) + '\n</span>');
            el[0].scrollTop = el[0].scrollHeight;
        }
    }
}

function handleCommandResult(evt) {
    if (isActiveEvent(evt)) {
        var key = "dev_" + evt.device_id;
        var row = getOrCreateRow(evt.device_id, evt.command_id);
        if (!row.summary) row.summary = '';
        if (evt.error_output) row.summary += evt.error_output + '\n';
        updateRowSummary(key, row.summary);
        updateRowStatus(key, evt.status || "completed", evt.duration_ms);
        sessionCompleted++;
        updateProgress();
        if (activeRowKey === key) showCommandResultDetail(key); // refresh visible detail
        loadCommandHistory();
        return;
    }
    // Watching from history
    if (watchingCmdId && evt.command_id) {
        if (evt.command_id === watchingCmdId) {
            watchingCmdId = null;
            if (watchPollTimer) { clearInterval(watchPollTimer); watchPollTimer = null; }
        }
        $.getJSON("/api/commands/" + evt.command_id, function(cmd) {
            renderStaticOutput(cmd);
            loadCommandHistory();
        });
    }
}

// ---- History ----
function loadCommandHistory() {
    $.getJSON("/api/commands?limit=30", function(cmds) {
        var rows = cmds.length === 0
            ? '<tr><td colspan="6" class="empty-state">暂无命令记录</td></tr>'
            : cmds.map(function(c) {
                var target = c.target_type === "broadcast" ? "全部" : ("#" + (c.target_id || '?'));
                return '<tr class="clickable-row" onclick="showCommandDetail(' + c.id + ')">' +
                    '<td>#' + c.id + '</td><td>' + c.created_at + '</td><td>' + target + '</td>' +
                    '<td><code>' + escapeHtml(c.command.substring(0, 50)) + (c.command.length > 50 ? '...' : '') + '</code></td>' +
                    '<td><span class="badge badge-' + c.status + '">' + statusLabel(c.status) + '</span></td>' +
                    '<td>' + (c.duration_ms > 0 ? (c.duration_ms + 'ms') : '-') + '</td>' +
                '</tr>';
            }).join("");
        $("#history-tbody").html(rows);
    });
}

function showCommandDetail(cmdID) {
    watchingCmdId = null;
    if (watchPollTimer) { clearInterval(watchPollTimer); watchPollTimer = null; }
    activeBatch = null;
    activeRowKey = null;
    sessionTotal = 0;
    sessionCompleted = 0;
    $("#result-progress").text('');

    $.getJSON("/api/commands/" + cmdID, function(cmd) {
        if (cmd.command) {
            if (cmdEditor) cmdEditor.setValue(cmd.command);
            else $("#command-editor").val(cmd.command);
        }
        renderStaticOutput(cmd);
        var running = (cmd.status === "dispatched" || cmd.status === "running" || cmd.status === "pending");
        if (running) watchingCmdId = cmdID;
    });
}

function renderStaticOutput(cmd) {
    var running = (cmd.status === "dispatched" || cmd.status === "running" || cmd.status === "pending");
    var html = '';

    if (running) {
        html += '<div style="margin-bottom:8px;">';
        html += '<button class="btn btn-danger btn-sm" onclick="cancelWatchedCommand(' + cmd.id + ')">⏹ 终止</button>';
        html += ' <span class="badge badge-' + cmd.status + '">' + statusLabel(cmd.status) + '</span>';
        html += '</div>';
    } else {
        html += '<div style="margin-bottom:8px;">';
        html += '<span class="badge badge-' + cmd.status + '">' + statusLabel(cmd.status) + '</span>';
        if (cmd.duration_ms) html += ' <small style="color:var(--text-secondary)">' + cmd.duration_ms + 'ms</small>';
        html += '</div>';
    }

    if (cmd.children && cmd.children.length > 0) {
        // Reset detail store for this history view.
        staticDetailData = {};
        // Render children as clickable result table.
        html += '<div class="result-summary" style="display:flex;flex-direction:column;flex:1;min-height:0;">';
        html += '<div style="font-size:12px;color:var(--text-secondary);margin-bottom:4px;">' + cmd.children.length + ' 台设备</div>';
        html += '<div style="flex:1;overflow-y:auto;">';
        html += '<table class="result-table"><thead><tr><th class="col-device">设备</th><th class="col-status">状态</th><th class="col-summary">输出</th><th class="col-duration">耗时</th></tr></thead><tbody>';
        cmd.children.forEach(function(child) {
            var fullOut = child.output || child.error_output || '';
            var did = child.target_id || '?';
            staticDetailData[did] = fullOut;
            var summary = fullOut.replace(/\n/g, ' ').substring(0, 80);
            if (!summary) summary = '<span style="color:var(--text-secondary)">(无输出)</span>';
            html += '<tr class="result-row ' + child.status + '" data-device-id="' + did + '">' +
                '<td class="col-device"><strong>#' + did + '</strong></td>' +
                '<td class="col-status"><span class="badge badge-' + child.status + '">' + statusLabel(child.status) + '</span></td>' +
                '<td class="col-summary" style="font-size:12px;">' + summary + '</td>' +
                '<td class="col-duration">' + (child.duration_ms ? (child.duration_ms + 'ms') : '-') + '</td>' +
            '</tr>';
        });
        html += '</tbody></table>';
        html += '</div>';
        html += '<div id="result-detail" style="display:none;margin-top:8px;"></div>';
        html += '</div>';
    } else {
        var out = cmd.output || cmd.error_output || '';
        html += '<div class="command-output" style="max-height:400px;">';
        html += out ? escapeHtml(out) : '<span style="color:var(--text-secondary)">(无输出)</span>';
        html += '</div>';
    }

    $("#result-area").html(html);

    // Click to expand full output for history rows.
    $("#result-area .result-row").off("click").on("click", function() {
        var did = $(this).data("device-id");
        var fullOut = staticDetailData[did] || '';
        $(".result-row").removeClass("active");
        $(this).addClass("active");
        var detail = $("#result-detail");
        detail.show();
        detail.html(
            '<div class="result-detail-header">' +
                '<span class="result-detail-device">设备 #' + did + ' 输出详情</span>' +
                '<div><button class="btn btn-sm" onclick="closeStaticDetail()">✕</button></div>' +
            '</div>' +
            '<div class="command-output" style="max-height:300px;">' + (fullOut ? escapeHtml(fullOut) : '<span style="color:var(--text-secondary)">(无输出)</span>') + '</div>'
        );
    });
}

function closeStaticDetail() {
    $("#result-detail").hide();
    $(".result-row").removeClass("active");
}

function cancelWatchedCommand(cmdId) {
    $.ajax({ url: "/api/commands/" + cmdId + "/cancel", method: "POST" });
}

function clearCommandHistory() {
    if (!confirm("确定要清空所有命令历史？")) return;
    $.ajax({
        url: "/api/commands/clear", method: "POST",
        success: function() {
            activeBatch = null;
            activeRowKey = null;
            $("#result-area").html('<div class="empty-state">选择目标设备并点击执行，或选择历史命令查看结果</div>');
            $("#result-progress").text('');
            loadCommandHistory();
        }
    });
}
