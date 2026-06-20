// ICPC Remote Control - File Distribution Admin Script
"use strict";

var selectedFiles = [];
var activeTaskPollInterval = null;
var postCmdEditor = null;

function loadDistribute() {
    selectedTargets = []; // reset global device selections
    deviceFilter = "";    // reset filter
    selectedFiles = [];   // reset files selection

    if (activeTaskPollInterval) {
        clearInterval(activeTaskPollInterval);
        activeTaskPollInterval = null;
    }

    refreshDistributeData();
}

function refreshDistributeData() {
    $.when(
        $.getJSON("/api/distribution/status"),
        $.getJSON("/api/distribution/files"),
        $.getJSON("/api/devices")
    ).done(function(statusResp, filesResp, devicesResp) {
        var activeTask = statusResp[0];
        var files = filesResp[0];
        allDevices = devicesResp[0];

        if (activeTask && (activeTask.status === "running" || activeTask.status === "completed" || activeTask.status === "stopped")) {
            renderActiveTask(activeTask);
            // Start polling if not already started and task is running
            if (activeTask.status === "running") {
                if (!activeTaskPollInterval) {
                    activeTaskPollInterval = setInterval(pollTaskStatus, 1000);
                }
            } else {
                if (activeTaskPollInterval) {
                    clearInterval(activeTaskPollInterval);
                    activeTaskPollInterval = null;
                }
            }
        } else {
            if (activeTaskPollInterval) {
                clearInterval(activeTaskPollInterval);
                activeTaskPollInterval = null;
            }
            renderSetupPage(files, activeTask ? activeTask.suggested_ip : "");
        }
    }).fail(function() {
        $("#content").html('<div class="empty-state">加载分发页面失败，请重试</div>');
    });
}

function pollTaskStatus() {
    $.getJSON("/api/distribution/status", function(task) {
        if (!task) return;
        if (task.status !== "running") {
            if (activeTaskPollInterval) {
                clearInterval(activeTaskPollInterval);
                activeTaskPollInterval = null;
            }
            renderActiveTask(task);
        } else {
            updateActiveTaskUI(task);
        }
    });
}

// ---- Setup Page Render (Idle State) ----

function renderSetupPage(files, suggestedIP) {
    files = files || [];
    var fileRows = files.length === 0
        ? '<tr><td colspan="4" class="empty-state">服务器暂无上传文件，请在上方上传</td></tr>'
        : files.map(function(f) {
            var isChecked = selectedFiles.indexOf(f.name) >= 0 ? "checked" : "";
            return '<tr>' +
                '<td style="width: 40px; text-align: center;">' +
                    '<input type="checkbox" class="file-select-cb" data-filename="' + escapeHtml(f.name) + '" ' + isChecked + '>' +
                '</td>' +
                '<td><strong>' + escapeHtml(f.name) + '</strong></td>' +
                '<td>' + formatBytes(f.size) + '</td>' +
                '<td>' + f.mod_time + '</td>' +
                '</tr>';
        }).join("");

    var html = '' +
        '<div class="page-header">' +
            '<h2 class="section-title" style="margin:0;">P2P 文件分发</h2>' +
        '</div>' +
        '<div class="command-layout">' +
            '<div class="command-panel" style="display:flex; flex-direction:column; gap:16px;">' +
                '<div class="panel-title">1. 文件管理</div>' +
                
                // Upload Form
                '<div style="border: 1px dashed var(--border); border-radius: 8px; padding: 16px; text-align: center;">' +
                    '<p style="font-size:13px; color:var(--text-secondary); margin-bottom:8px;">向主服务器上传文件（支持大文件分块上传）</p>' +
                    '<input type="file" id="upload-file-input" style="display:none;">' +
                    '<button class="btn btn-outline btn-sm" onclick="$(\'#upload-file-input\').click()">+ 选择并上传文件</button>' +
                    '<div id="upload-progress-container" style="display:none; margin-top:12px;">' +
                        '<div class="mem-bar">' +
                            '<div id="upload-progress-fill" class="mem-bar-fill" style="width:0%"></div>' +
                            '<div id="upload-progress-text" class="mem-bar-text">0%</div>' +
                        '</div>' +
                    '</div>' +
                '</div>' +

                // Files Table
                '<div class="table-container" style="max-height: 250px; overflow-y: auto;">' +
                    '<table>' +
                        '<thead>' +
                            '<tr>' +
                                '<th style="width:40px; text-align:center;"><input type="checkbox" id="file-select-all"></th>' +
                                '<th>文件名</th><th>大小</th><th>修改时间</th>' +
                            '</tr>' +
                        '</thead>' +
                        '<tbody>' + fileRows + '</tbody>' +
                    '</table>' +
                '</div>' +

                // Batch Actions
                '<div style="display:flex; gap:8px; flex-wrap:wrap; font-size:12px;">' +
                    '<button class="btn btn-sm btn-outline" onclick="selectAllFiles(true)">全选</button>' +
                    '<button class="btn btn-sm btn-outline" onclick="selectAllFiles(false)">取消全选</button>' +
                    '<button class="btn btn-sm btn-danger" onclick="deleteSelectedFiles()">删除选中</button>' +
                    '<button class="btn btn-sm btn-danger" style="background:#8b0000;" onclick="clearAllServerFiles()">清空服务器</button>' +
                '</div>' +

                // Save Directory Config
                '<div style="display:flex; flex-direction:column; gap:6px;">' +
                    '<label style="font-weight:600; font-size:13px;">2. 客户端目标保存目录</label>' +
                    '<input type="text" id="target-save-dir" placeholder="例如: /home/cwxu/Downloads" value="/home/cwxu/Downloads">' +
                    '<small style="color:var(--text-secondary); font-size:11px;">下载的文件将自动以原文件名保存于该目录下，文件夹如不存在将自动创建。</small>' +
                '</div>' +

                // Server IP Config
                '<div style="display:flex; flex-direction:column; gap:6px;">' +
                    '<label style="font-weight:600; font-size:13px;">3. 分发服务器 IP (或主机名)</label>' +
                    '<input type="text" id="distribute-server-ip" placeholder="例如: ' + escapeHtml(suggestedIP || location.hostname || "192.168.1.100") + '" value="' + escapeHtml(localStorage.getItem("distribute_server_ip") || suggestedIP || location.hostname || "") + '">' +
                    '<small style="color:var(--text-secondary); font-size:11px;">指定主控服务器在局域网中的 IP 或主机名，留空则默认使用系统自动探测的 IP (<code>' + escapeHtml(suggestedIP || location.hostname || "无") + '</code>)。</small>' +
                    '<div style="display:flex; margin-top:4px;">' +
                        '<button class="btn btn-sm btn-outline" style="flex:1; justify-content:center;" onclick="testDistributionConnectivity()">⚡ 测试客户端连接 (多机房分发必测)</button>' +
                    '</div>' +
                    '<div id="precheck-results-container" style="display:none; font-size:12px; border:1px solid var(--border); border-radius:6px; padding:10px; background:var(--bg-secondary); max-height:150px; overflow-y:auto; margin-top:4px;"></div>' +
                '</div>' +

                // Action Start Trigger
                '<div style="margin-top:10px;">' +
                    '<button class="btn btn-primary" style="width:100%; justify-content:center;" onclick="startFileDistribution()">▶ 开启 P2P 分发</button>' +
                '</div>' +
            '</div>' +

            // Target selector & Post execution command editor (right column)
            '<div class="command-panel" style="display:flex; flex-direction:column; gap:16px;">' +
                '<div style="display:flex; flex-direction:column;">' +
                    '<div class="panel-title">4. 目标接收设备</div>' +
                    '<div id="device-selector-container" style="max-height: 200px; overflow-y: auto; border: 1px solid var(--border); border-radius: 6px; padding: 10px;"></div>' +
                '</div>' +
                '<div style="display:flex; flex-direction:column; gap:6px;">' +
                    '<div class="panel-title" style="margin:0;">6. 分发后执行命令 (选填)</div>' +
                    '<textarea id="distribute-post-cmd-editor"></textarea>' +
                    '<small style="color:var(--text-secondary); font-size:11px; margin-top:2px;">文件下载成功后在选手端自动执行。运行的工作目录为上述目标保存目录。</small>' +
                '</div>' +
            '</div>' +
        '</div>';

    $("#content").html(html);

    // Initialise device list selector
    renderDeviceList();
    setTimeout(initPostCmdCodeMirror, 100);

    // Bind checkboxes events
    $("#file-select-all").on("change", function() {
        var checked = $(this).is(":checked");
        $(".file-select-cb").prop("checked", checked).trigger("change");
    });

    $(".file-select-cb").on("change", function() {
        var filename = $(this).data("filename");
        var checked = $(this).is(":checked");
        var idx = selectedFiles.indexOf(filename);
        if (checked && idx < 0) {
            selectedFiles.push(filename);
        } else if (!checked && idx >= 0) {
            selectedFiles.splice(idx, 1);
        }
        updateSelectAllCheckbox();
    });

    // Upload change event
    $("#upload-file-input").on("change", handleFileUpload);
}

function selectAllFiles(val) {
    $(".file-select-cb").prop("checked", val).trigger("change");
    $("#file-select-all").prop("checked", val);
}

function updateSelectAllCheckbox() {
    var allChecked = $(".file-select-cb").length > 0 && $(".file-select-cb:not(:checked)").length === 0;
    $("#file-select-all").prop("checked", allChecked);
}

// ---- File Upload Management ----

function handleFileUpload() {
    var file = document.getElementById("upload-file-input").files[0];
    if (!file) return;

    var formData = new FormData();
    formData.append("file", file);

    $("#upload-progress-container").show();
    $("#upload-progress-fill").css("width", "0%");
    $("#upload-progress-text").text("0%");

    $.ajax({
        url: "/api/distribution/upload",
        type: "POST",
        data: formData,
        processData: false,
        contentType: false,
        xhr: function() {
            var xhr = new window.XMLHttpRequest();
            xhr.upload.addEventListener("progress", function(evt) {
                if (evt.lengthComputable) {
                    var percentComplete = Math.round((evt.loaded / evt.total) * 100);
                    $("#upload-progress-fill").css("width", percentComplete + "%");
                    $("#upload-progress-text").text(percentComplete + "%");
                }
            }, false);
            return xhr;
        },
        success: function(res) {
            $("#upload-progress-container").hide();
            document.getElementById("upload-file-input").value = "";
            refreshDistributeData();
        },
        error: function(xhr) {
            $("#upload-progress-container").hide();
            document.getElementById("upload-file-input").value = "";
            var err = "上传文件失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function deleteSelectedFiles() {
    if (selectedFiles.length === 0) {
        alert("请先选中要删除的文件");
        return;
    }
    if (!confirm("确定要从主控服务器上删除这 " + selectedFiles.length + " 个文件吗？")) return;

    $.ajax({
        url: "/api/distribution/delete",
        method: "POST",
        contentType: "application/json",
        data: JSON.stringify({ filenames: selectedFiles }),
        success: function() {
            selectedFiles = [];
            refreshDistributeData();
        },
        error: function(xhr) {
            alert("删除文件失败");
        }
    });
}

function clearAllServerFiles() {
    if (!confirm("⚠️ 确定要清空主控服务器分发夹下的【所有】文件吗？该操作不可逆！")) return;

    $.ajax({
        url: "/api/distribution/clear",
        method: "POST",
        success: function() {
            selectedFiles = [];
            refreshDistributeData();
        },
        error: function(xhr) {
            alert("清空文件失败");
        }
    });
}

// ---- Active Task Render (Running State) ----

function renderActiveTask(task) {
    var totalFiles = task.files.length;
    var currentFile = task.active_file;
    var fileIdx = task.active_idx + 1;

    var actionBtn = '';
    if (task.status === "running") {
        actionBtn = '<button class="btn btn-danger btn-sm" onclick="stopFileDistribution()">⏹ 停止分发</button>';
    } else {
        actionBtn = '<button class="btn btn-primary btn-sm" onclick="clearLastDistributionTask()">← 返回配置</button>';
    }

    var html = '' +
        '<div class="page-header">' +
            '<h2 class="section-title" style="margin:0;">P2P 分发监控面板</h2>' +
            actionBtn +
        '</div>' +
        
        // Progress Card
        '<div class="settings-card" style="margin-bottom:20px;">' +
            '<h3>正在分发：<code style="background:var(--bg-primary); padding:2px 6px; border-radius:4px; font-size:15px; color:var(--accent);">' + escapeHtml(currentFile) + '</code></h3>' +
            '<p class="settings-desc" style="margin-top:6px;">' +
                '文件序号: ' + fileIdx + ' / ' + totalFiles + ' | ' +
                '目标保存目录: <code>' + escapeHtml(task.save_dir) + '</code>' +
                (task.server_ip ? ' | 分发服务器 IP: <code>' + escapeHtml(task.server_ip) + '</code>' : '') +
                (task.post_cmd ? ' | 后置命令: <code>' + escapeHtml(task.post_cmd) + '</code>' : '') +
            '</p>' +
            '<div style="margin-top:12px;">' +
                '<div class="mem-bar" style="height:18px;">' +
                    '<div id="task-overall-progress" class="mem-bar-fill" style="width:0%"></div>' +
                    '<div id="task-overall-text" class="mem-bar-text" style="line-height:18px;">正在统计终端下载进度...</div>' +
                '</div>' +
            '</div>' +
        '</div>' +

        // Devices Detail Progress Table
        '<div class="table-container">' +
            '<table>' +
                '<thead>' +
                    '<tr>' +
                        '<th style="width:80px;">设备</th>' +
                        '<th>主机名</th>' +
                        '<th style="min-width:180px;">分块同步进度</th>' +
                        '<th style="width:100px;">速度</th>' +
                        '<th style="width:110px;">状态</th>' +
                        '<th>提示/错误</th>' +
                        '<th style="width:90px; text-align:center;">操作</th>' +
                    '</tr>' +
                '</thead>' +
                '<tbody id="dist-progress-tbody"></tbody>' +
            '</table>' +
        '</div>';

    $("#content").html(html);
    updateActiveTaskUI(task);
}

function updateActiveTaskUI(task) {
    var tbody = $("#dist-progress-tbody");
    if (!tbody.length) return;

    var progresses = Object.values(task.progresses || {});
    // Sort by device_id
    progresses.sort(function(a, b) { return a.device_id - b.device_id; });

    var totalDevices = progresses.length;
    var completedDevices = 0;
    var failedDevices = 0;

    var rowsHtml = progresses.map(function(p) {
        var pct = Math.round(p.percentage || 0);
        var speed = p.speed_mbps > 0 ? (p.speed_mbps + " Mbps") : "-";
        
        var statusClass = "badge-running";
        var statusLabel = "下载中";
        if (p.status === "completed") {
            statusClass = "badge-completed";
            statusLabel = "成功";
            completedDevices++;
        } else if (p.status === "failed") {
            statusClass = "badge-failed";
            statusLabel = "失败 ❌";
            failedDevices++;
        } else if (p.status === "cancelled") {
            statusClass = "badge-failed";
            statusLabel = "已取消 ⚠️";
        } else if (p.status === "idle") {
            statusClass = "badge-pending";
            statusLabel = "等待中";
        }

        var isFailed = p.status === "failed";
        var actionBtn = isFailed
            ? '<button class="btn btn-sm btn-primary btn-outline" style="padding:2px 8px;font-size:11px;" onclick="retryDeviceDistribution(' + p.device_id + ')">重新分发</button>'
            : '-';

        // Error display
        var errText = p.error ? '<span style="color:var(--danger);font-size:12px;">' + escapeHtml(p.error) + '</span>' : '-';

        var chunksLabel = p.total_chunks > 0 ? (' (' + p.downloaded + '/' + p.total_chunks + ' 块)') : '';

        // Progress bar inside table cell
        var pBar = '<div class="mem-bar" style="height: 14px;">' +
            '<div class="mem-bar-fill ' + (isFailed ? 'critical' : '') + '" style="width:' + pct + '%"></div>' +
            '<div class="mem-bar-text" style="line-height: 14px; font-size:10px;">' + pct + '%' + chunksLabel + '</div>' +
            '</div>';

        return '<tr>' +
            '<td><strong>#' + p.device_id + '</strong></td>' +
            '<td>' + escapeHtml(p.hostname || ('设备 #' + p.device_id)) + '</td>' +
            '<td>' + pBar + '</td>' +
            '<td>' + speed + '</td>' +
            '<td><span class="badge ' + statusClass + '">' + statusLabel + '</span></td>' +
            '<td>' + errText + '</td>' +
            '<td style="text-align:center;">' + actionBtn + '</td>' +
            '</tr>';
    }).join("");

    tbody.html(rowsHtml);

    // Update overall progress bar
    var overallPct = totalDevices > 0 ? Math.round((completedDevices / totalDevices) * 100) : 0;
    $("#task-overall-progress").css("width", overallPct + "%");
    var progressText = "终端分发完成度: " + completedDevices + " / " + totalDevices;
    if (failedDevices > 0) {
        progressText += " | 失败: " + failedDevices + " 台";
    }
    if (overallPct === 100) {
        progressText += " ✓ 全部分发完成";
        $("#task-overall-progress").addClass("high");
    }
    $("#task-overall-text").text(progressText);
}

// WebSocket Live Events Delegate
function handleDistributeEvent(event, data) {
    if (event === "distribute_progress_update") {
        updateActiveTaskUI(data);
    } else if (event === "distribute_task_finished") {
        if (activeTaskPollInterval) {
            clearInterval(activeTaskPollInterval);
            activeTaskPollInterval = null;
        }
        renderActiveTask(data);
    }
}

// ---- Controller Actions ----

function startFileDistribution() {
    if (selectedFiles.length === 0) {
        alert("请先从文件列表中勾选至少一个要分发的文件！");
        return;
    }

    var saveDir = $("#target-save-dir").val().trim();
    if (!saveDir) {
        alert("请输入客户端的目标保存目录");
        return;
    }
    var serverIP = $("#distribute-server-ip").val().trim();
    localStorage.setItem("distribute_server_ip", serverIP);
    var postCmd = postCmdEditor ? postCmdEditor.getValue().trim() : "";
    localStorage.setItem("distribute_post_cmd", postCmd);

    // Resolve target IDs. 
    // selectedTargets is populated by device-selector.js
    // if empty, it defaults to all online (broadcast) which our backend handles automatically.
    var targets = selectedTargets;
    var targetLabel = targets.length === 0 ? "所有在线设备（广播）" : (targets.length + " 台选中设备");

    if (!confirm("确定开始将以下文件分发到 " + targetLabel + " 吗？\n\n文件列表:\n" + selectedFiles.join("\n") + "\n\n保存目录: " + saveDir + (serverIP ? "\n分发服务器 IP: " + serverIP : "") + (postCmd ? "\n分发后执行命令: " + postCmd : ""))) return;

    $.ajax({
        url: "/api/distribution/start",
        method: "POST",
        contentType: "application/json",
        data: JSON.stringify({
            files: selectedFiles,
            save_dir: saveDir,
            target_ids: targets,
            server_ip: serverIP,
            post_cmd: postCmd
        }),
        success: function(task) {
            refreshDistributeData();
        },
        error: function(xhr) {
            var err = "启动分发失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function stopFileDistribution() {
    if (!confirm("⏹ 确定要强制中断当前正在运行的文件分发任务吗？\n\n注意：这会向所有选手机下发停止指令，清理局域网 P2P 共享连接。")) return;

    $.ajax({
        url: "/api/distribution/stop",
        method: "POST",
        success: function() {
            refreshDistributeData();
        },
        error: function(xhr) {
            alert("停止分发失败");
        }
    });
}

function retryDeviceDistribution(deviceID) {
    $.ajax({
        url: "/api/distribution/retry",
        method: "POST",
        contentType: "application/json",
        data: JSON.stringify({ device_id: deviceID }),
        success: function() {
            // UI will update reactively via Websocket progress report
        },
        error: function(xhr) {
            var err = "重新分发失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            alert(err);
        }
    });
}

function testDistributionConnectivity() {
    var serverIP = $("#distribute-server-ip").val().trim();
    localStorage.setItem("distribute_server_ip", serverIP);
    var targets = selectedTargets;
    var targetLabel = targets.length === 0 ? "所有在线设备" : (targets.length + " 台选中设备");

    $("#precheck-results-container").show().html(
        '<div style="color:var(--text-secondary); text-align:center; padding: 10px 0;">' +
        '正在发送连接指令并等待 ' + targetLabel + ' 握手响应，请稍候 (约 3 秒)...' +
        '</div>'
    );

    $.ajax({
        url: "/api/distribution/precheck",
        method: "POST",
        contentType: "application/json",
        data: JSON.stringify({
            server_ip: serverIP,
            target_ids: targets
        }),
        success: function(results) {
            renderPrecheckResults(results);
        },
        error: function(xhr) {
            var err = "测试连接失败";
            try { err = JSON.parse(xhr.responseText).error || err; } catch(e) {}
            $("#precheck-results-container").html('<div style="color:var(--danger); font-weight:600; text-align:center; padding: 10px 0;">' + escapeHtml(err) + '</div>');
        }
    });
}

function renderPrecheckResults(results) {
    var container = $("#precheck-results-container");
    if (!results || results.length === 0) {
        container.html('<div style="color:var(--text-secondary); text-align:center; padding:10px 0;">没有在线的目标设备可测试</div>');
        return;
    }

    var successCount = 0;
    var failedCount = 0;

    var itemsHtml = results.map(function(r) {
        var name = "设备 #" + r.device_id;
        if (r.success) {
            successCount++;
            return '<div style="color:var(--success); display:flex; justify-content:space-between; margin-bottom:4px; font-weight: 500;">' +
                '<span>✓ ' + escapeHtml(name) + '</span>' +
                '<span>可达 (OK)</span>' +
                '</div>';
        } else {
            failedCount++;
            return '<div style="color:var(--danger); display:flex; flex-direction:column; margin-bottom:6px;">' +
                '<div style="display:flex; justify-content:space-between; font-weight: 500;">' +
                    '<span>✗ ' + escapeHtml(name) + '</span>' +
                    '<span>无法访问</span>' +
                '</div>' +
                '<div style="font-size:11px; opacity:0.8; padding-left:12px; margin-top:2px;">原因: ' + escapeHtml(r.error || "未知错误") + '</div>' +
                '</div>';
        }
    }).join("");

    var summaryHtml = '<div style="font-weight:600; border-bottom:1px solid var(--border); padding-bottom:6px; margin-bottom:8px; display:flex; justify-content:space-between;">' +
        '<span>连接测试完成</span>' +
        '<span>成功: <span style="color:var(--success);">' + successCount + '</span> | 失败: <span style="color:var(--danger);">' + failedCount + '</span></span>' +
        '</div>';

    container.html(summaryHtml + itemsHtml);
}

function clearLastDistributionTask() {
    $.ajax({
        url: "/api/distribution/reset",
        method: "POST",
        success: function() {
            refreshDistributeData();
        },
        error: function(xhr) {
            alert("重置分发任务状态失败");
        }
    });
}

function initPostCmdCodeMirror() {
    if (postCmdEditor) {
        var prevEl = postCmdEditor.getWrapperElement();
        if (prevEl && prevEl.parentNode) postCmdEditor.toTextArea();
        postCmdEditor = null;
    }

    var ta = document.getElementById("distribute-post-cmd-editor");
    if (!ta) return;

    if (ta.classList.contains("CodeMirror")) return;
    if (ta.nextSibling && ta.nextSibling.classList && ta.nextSibling.classList.contains("CodeMirror")) return;

    var savedPostCmd = localStorage.getItem("distribute_post_cmd") || "";

    postCmdEditor = CodeMirror.fromTextArea(ta, {
        mode: "shell", theme: "eclipse", lineNumbers: true,
        lineWrapping: true, indentUnit: 2, tabSize: 2,
        matchBrackets: true, autoCloseBrackets: true
    });
    postCmdEditor.setValue(savedPostCmd);
    postCmdEditor.setSize(null, "115px");
}
