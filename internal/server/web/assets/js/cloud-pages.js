"use strict";

var pendingCloudDevice = null;

function isCloudAllRooms() {
    return deploymentConfig.mode === 'cloud' && !selectedRoom;
}

function fleetDeviceSelectorHTML() {
    return '<section class="settings-card"><h3>目标设备</h3><div class="ops-toolbar"><select id="fleet-room" aria-label="机房筛选"><option value="">全部机房</option></select><input type="search" id="fleet-search" placeholder="搜索机房、设备号、主机名、选手"><button class="btn btn-outline" id="fleet-select-online">选择筛选内在线设备</button><button class="btn btn-outline" id="fleet-select-all">选择全部筛选设备</button><button class="btn btn-outline" id="fleet-clear">清空选择</button></div><p id="fleet-selection-count" class="settings-desc"></p><div class="table-container"><table><thead><tr><th>选择</th><th>机房 / 设备号</th><th>主机名 / 系统</th><th>健康</th><th>选手</th><th>签到</th><th>状态</th><th>操作</th></tr></thead><tbody id="fleet-devices"></tbody></table></div></section>';
}

function bindFleetSelector() {
    $('#fleet-search').val(fleetFilter).on('input', function() { fleetFilter = this.value; renderFleetDevices(); });
    $('#fleet-room').on('change', function() { fleetRoomFilter = this.value; renderFleetDevices(); });
    $('#fleet-select-online').on('click', function() { visibleFleetDevices().forEach(function(device) { if (device.connected) fleetSelection.add(device.key); }); renderFleetDevices(); });
    $('#fleet-select-all').on('click', function() { visibleFleetDevices().forEach(function(device) { fleetSelection.add(device.key); }); renderFleetDevices(); });
    $('#fleet-clear').on('click', function() { fleetSelection.clear(); renderFleetDevices(); });
    $('#fleet-devices').on('change', '.fleet-select', function() { if (this.checked) fleetSelection.add(this.value); else fleetSelection.delete(this.value); renderFleetDevices(); });
    $('#fleet-devices').on('click', '.fleet-manage', function() {
        var room = this.dataset.room;
        if (currentPage !== 'checkin') pendingCloudDevice = {room:room,device:Number(this.dataset.device)};
        enterRoom(room, currentPage === 'checkin' ? 'checkin' : 'devices');
    });
    renderFleetDevices();
}

function fleetJobsHTML() {
    return '<section class="settings-card"><h3>跨机房投递记录</h3><p class="settings-desc">“已接收”表示中转接受操作，不等于选手机执行成功。下方可查看回执；选手机结果见命令记录或机房分发详情。离线中转恢复后自动投递。</p><div id="fleet-jobs"></div></section>';
}

function loadCloudPage(page) {
    if (['dashboard', 'devices', 'commands', 'distribute', 'checkin', 'network', 'power'].indexOf(page) < 0) return false;
    var html = '<div class="page-header"><div><h2>' + PAGE_TITLES[page] + '</h2><p class="ops-intro">全部机房 · 顶部可切换单个机房，切换后仍留在当前功能。设备以「机房 + 编号」区分。</p></div>';
    if (page === 'devices' || page === 'checkin') html += '<button class="btn btn-outline" onclick="exportFleetCSV()">导出设备与签到 · CSV</button>';
    html += '</div>';
    if (page === 'dashboard') {
        html += '<div id="cloud-dashboard-stats" class="stats-grid"></div><section class="settings-card"><h3>进行中任务</h3><div id="cloud-distributions"></div></section>' + snapshotPanelHTML() + cloudCommandHistoryHTML();
    } else {
        if (page === 'checkin') html += '<div id="checkin-stats-container"></div>';
        html += fleetDeviceSelectorHTML();
        if (page === 'devices') {
            html += '<div class="ops-toolbar"><button class="btn btn-primary" onclick="navigateTo(\'commands\')">向所选设备执行命令</button><button class="btn btn-outline" onclick="navigateTo(\'distribute\')">向所选设备分发文件</button><button class="btn btn-outline" onclick="navigateTo(\'power\')">电源操作</button></div>';
        } else if (page === 'commands') {
            html += '<section class="settings-card"><h3>执行命令</h3><div id="cloud-presets" class="ops-toolbar"></div><textarea id="fleet-command" rows="6" style="width:100%" placeholder="例如：hostname && uptime"></textarea><div class="ops-toolbar"><button class="btn btn-primary" onclick="queueFleetOperation(\'command\')">向已选设备执行</button></div></section>' + cloudCommandHistoryHTML() + fleetJobsHTML();
        } else if (page === 'distribute') {
            html += '<section class="settings-card"><h3>云端文件库</h3><p class="settings-desc">云端保存一份文件。开始分发后，每个机房从云端拉取（SHA-256 校验），再在机房内 P2P 分发到选手机；离线机房恢复后自动投递。</p>' +
                '<div class="ops-toolbar"><label class="btn btn-outline">上传文件 <input type="file" id="cloud-file-input" style="display:none"></label><span id="cloud-upload-status" role="status" class="settings-desc"></span></div>' +
                '<div id="cloud-upload-progress" class="mem-bar" style="display:none;margin:8px 0;"><div id="cloud-upload-fill" class="mem-bar-fill" style="width:0%"></div><div id="cloud-upload-text" class="mem-bar-text">0%</div></div>' +
                '<div class="table-container" style="max-height:320px;overflow-y:auto;"><table><thead><tr><th style="width:40px;text-align:center;"><input type="checkbox" id="cloud-file-select-all"></th><th>文件名</th><th>大小</th><th>修改时间</th></tr></thead><tbody id="fleet-files"></tbody></table></div>' +
                '<div class="ops-toolbar"><button class="btn btn-sm btn-outline" onclick="selectAllCloudFiles(true)">全选</button><button class="btn btn-sm btn-outline" onclick="selectAllCloudFiles(false)">取消全选</button><button class="btn btn-sm btn-danger" onclick="deleteSelectedCloudFiles()">删除选中</button><button class="btn btn-sm btn-danger" onclick="clearCloudFiles()">清空云端文件库</button></div>' +
                '<div class="ops-form"><label>选手机保存目录<input id="fleet-save-dir" value="/tmp/icpc-downloads"></label><label>全部文件完成后的命令<input id="fleet-post-cmd" placeholder="选填"></label></div>' +
                '<div class="ops-toolbar"><button class="btn btn-primary" onclick="queueFleetOperation(\'distribute\')">向已选设备分发</button></div></section>' +
                '<section class="settings-card"><h3>各机房分发进度</h3><div id="cloud-distributions"></div></section>' + fleetJobsHTML();
        } else if (page === 'network') {
            html += '<section class="settings-card"><h3>统一白名单规则</h3><p class="settings-desc">在云端编辑规则，再下发到已选设备。不会使用空的本地设备列表。</p><div id="rules-list"></div><button id="btn-add-rule" class="btn btn-outline">添加规则</button><div id="rules-result"></div><div class="ops-toolbar"><button id="cloud-network-apply" class="btn btn-danger">应用网络限制</button><button class="btn btn-outline" onclick="queueFleetOperation(\'network_remove\')">解除网络限制</button></div></section>' + fleetJobsHTML();
        } else if (page === 'power') {
            html += '<section class="settings-card"><h3>立即操作</h3><div class="ops-toolbar"><button class="btn btn-primary" onclick="queueFleetOperation(\'wol\')">唤醒已选设备</button><button class="btn btn-danger" onclick="queueFleetOperation(\'command\',{command:\'shutdown -h now\'})">立即关机</button><button class="btn btn-outline" onclick="queueFleetOperation(\'command\',{command:\'reboot\'})">立即重启</button></div><h3>定时电源计划</h3><div class="ops-form"><label>操作<select id="cloud-power-action"><option value="shutdown">关机</option><option value="reboot">重启</option><option value="wol">唤醒</option></select></label><label>执行时间<input type="datetime-local" id="cloud-power-time"></label><label>备注<input id="cloud-power-note"></label></div><div class="ops-toolbar"><button id="cloud-power-schedule" class="btn btn-primary">为所选设备创建计划</button></div><p class="settings-desc">切换顶部机房范围可查看、删除该机房的电源计划。</p></section>' + fleetJobsHTML();
        }
    }
    $('#content').html(html);
    pendingCloudDevice = null;
    bindFleetSelector();
    if (page === 'commands') {
        $.getJSON('/api/presets', function(presets) {
            if (currentPage !== 'commands' || !isCloudAllRooms()) return;
            (presets || []).forEach(function(preset) {
                $('<button class="btn btn-sm btn-outline">').text(preset.name || preset.command).on('click', function() { $('#fleet-command').val(preset.command); }).appendTo('#cloud-presets');
            });
        });
    } else if (page === 'distribute') {
        loadCloudFiles();
        $('#cloud-file-input').on('change', uploadCloudFile);
        $('#cloud-file-select-all').on('change', function() { selectAllCloudFiles($(this).is(':checked')); });
    } else if (page === 'network') {
        $.getJSON('/api/network/rules', function(rules) {
            if (currentPage !== 'network' || !isCloudAllRooms()) return;
            networkRules = rules || [];
            renderRules();
        });
        $('#btn-add-rule').on('click', function() { networkRules.push({type:'DOMAIN-SUFFIX',value:''}); renderRules(); });
        $('#cloud-network-apply').on('click', function() {
            var rules = [];
            $('#rules-list .rule-row').each(function() {
                var value = $(this).find('.rule-value').val().trim();
                if (value) rules.push({type:$(this).find('.rule-type').val(),value:value});
            });
            queueFleetOperation('network_apply', {rules:rules});
        });
    } else if (page === 'power') {
        $('#cloud-power-schedule').on('click', function() {
            var value = $('#cloud-power-time').val();
            if (!value) { showToast('请选择执行时间', 'error'); return; }
            queueFleetOperation('schedule', {action:$('#cloud-power-action').val(),run_at:new Date(value).toISOString(),note:$('#cloud-power-note').val()});
        });
    }
    updateCloudPage();
    if (typeof loadSnapshotPanel === "function") loadSnapshotPanel();
    refreshRoomsData();
    refreshFleetJobs();
    return true;
}

function cloudCommandHistoryHTML() {
    return '<section class="settings-card"><h3>最近命令 · 各机房最近 10 条</h3><p class="settings-desc">中转每 5 秒上报；离线机房保留最后记录。点击结果可查看完整输出，更多历史可切换机房查询。</p><div class="table-container"><table><thead><tr><th>机房 / 命令</th><th>时间</th><th>目标</th><th>命令</th><th>状态</th><th>操作</th></tr></thead><tbody id="cloud-command-history"></tbody></table></div><div id="cloud-command-detail"></div></section>';
}

function updateCloudPage() {
    var total = 0, online = 0, checkedIn = 0, checkedOut = 0, commands = 0;
    var recent = [];
    fleetRooms.forEach(function(room) {
        total += room.devices.length;
        commands += room.total_commands || 0;
        room.devices.forEach(function(device) {
            if (device.connected) online++;
            if (device.checkin_status === 1) checkedIn++;
            if (device.checkin_status === 2) checkedOut++;
        });
        (room.recent_commands || []).forEach(function(command) { recent.push(Object.assign({}, command, {room_id:room.id,room_name:room.name})); });
    });
    var metrics = [['设备总数',total,'devices'],['在线设备',online,'devices'],['离线设备',total-online,'devices'],['已签到',checkedIn,'checkin'],['未签到',total-checkedIn-checkedOut,'checkin'],['命令总数',commands,'commands']];
    $('#cloud-dashboard-stats').html(metrics.map(function(metric) {
        return '<div class="stat-card" role="button" tabindex="0" data-destination="' + metric[2] + '"><div class="stat-value">' + metric[1] + '</div><div class="stat-label">' + metric[0] + '</div></div>';
    }).join('')).find('[data-destination]').on('click keydown', function(event) {
        if (event.type === 'click' || event.key === 'Enter') navigateTo(this.dataset.destination);
    });
    if ($('#checkin-stats-container').length) renderCheckinStats({total:total,checked_in:checkedIn,checked_out:checkedOut,not_checked:total-checkedIn-checkedOut});
    $('#cloud-distributions').html(fleetRooms.filter(function(room) { return room.distribution; }).map(function(room) {
        return '<div class="ops-note"><div class="ops-toolbar"><strong>' + escapeHtml(room.name) + '</strong><span>' + (room.online ? '中转在线' : '中转离线 · 最后上报') + '</span><button class="btn btn-sm btn-outline cloud-task-room" data-room="' + escapeHtml(room.id) + '">分发详情 / 补传</button></div>' + renderDistSummary(room.distribution) + '</div>';
    }).join('') || '<p class="settings-desc">暂无分发任务</p>');
    $('.cloud-task-room').on('click', function() { enterRoom(this.dataset.room, 'distribute'); });
    recent.sort(function(first, second) { return new Date(second.created_at) - new Date(first.created_at); });
    $('#cloud-command-history').html(recent.slice(0, 100).map(function(command) {
        return '<tr><td>' + escapeHtml(command.room_name) + ' / #' + command.id + '</td><td>' + escapeHtml(command.created_at) + '</td><td>' + (command.target_type === 'broadcast' ? '本机房全部' : '#' + command.target_id) + '</td><td><code>' + escapeHtml((command.command || '').slice(0, 100)) + '</code></td><td>' + escapeHtml(statusLabel(command.status)) + '</td><td><button class="btn btn-sm btn-outline cloud-command-result" data-room="' + escapeHtml(command.room_id) + '" data-command="' + command.id + '">结果</button></td></tr>';
    }).join('') || '<tr><td colspan="6" class="empty-state">暂无命令记录</td></tr>');
    $('.cloud-command-result').on('click', function() {
        $.getJSON('/api/cluster/rooms/' + encodeURIComponent(this.dataset.room) + '/proxy/commands/' + this.dataset.command, function(result) {
            $('#cloud-command-detail').html('<h3>命令 #' + result.id + '</h3><pre class="ops-result"></pre>').find('pre').text(JSON.stringify(result, null, 2));
        }).fail(function() { showToast('机房离线或命令结果暂不可用', 'error'); });
    });
}

function loadCloudFiles() {
    $.getJSON('/api/distribution/files', function(files) {
        var selected = new Set($('.fleet-file:checked').map(function() { return this.value; }).get());
        $('#fleet-files').html((files || []).map(function(file) {
            return '<tr><td style="text-align:center;"><input class="fleet-file" type="checkbox" value="' + escapeHtml(file.name) + '" ' + (selected.has(file.name) ? 'checked' : '') + '></td><td><strong>' + escapeHtml(file.name) + '</strong></td><td>' + formatBytes(file.size) + '</td><td>' + escapeHtml(file.mod_time || '') + '</td></tr>';
        }).join('') || '<tr><td colspan="4" class="empty-state">尚未上传文件</td></tr>');
        $('#cloud-file-select-all').prop('checked', false);
    }).fail(function() {
        $('#fleet-files').html('<tr><td colspan="4" class="empty-state">无法加载云端文件库</td></tr>');
    });
}

function selectAllCloudFiles(checked) {
    $('.fleet-file').prop('checked', checked);
    $('#cloud-file-select-all').prop('checked', checked);
}

function selectedCloudFileNames() {
    return $('.fleet-file:checked').map(function() { return this.value; }).get();
}

function deleteSelectedCloudFiles() {
    var names = selectedCloudFileNames();
    if (!names.length) { showToast('请先选中要删除的文件', 'error'); return; }
    if (!confirm('确定从云端文件库删除这 ' + names.length + ' 个文件吗？已分发到机房的副本不受影响。')) return;
    $.ajax({url:'/api/distribution/delete',method:'POST',contentType:'application/json',data:JSON.stringify({filenames:names})}).done(function() {
        showToast('已删除', 'success');
        loadCloudFiles();
    }).fail(function(request) {
        showToast((request.responseJSON && request.responseJSON.error) || '删除失败', 'error');
    });
}

function clearCloudFiles() {
    if (!confirm('确定清空云端文件库的全部文件吗？该操作不可逆。')) return;
    $.ajax({url:'/api/distribution/clear',method:'POST'}).done(function() {
        showToast('云端文件库已清空', 'success');
        loadCloudFiles();
    }).fail(function(request) {
        showToast((request.responseJSON && request.responseJSON.error) || '清空失败', 'error');
    });
}

function uploadCloudFile() {
    var file = this.files[0];
    if (!file) return;
    var form = new FormData();
    form.append('file', file);
    $('#cloud-file-input').prop('disabled', true);
    $('#cloud-upload-status').text('正在上传 ' + file.name);
    $('#cloud-upload-progress').show();
    $('#cloud-upload-fill').css('width', '0%');
    $('#cloud-upload-text').text('0%');
    $.ajax({
        url: '/api/distribution/upload', method: 'POST', data: form, processData: false, contentType: false,
        xhr: function() {
            var xhr = new window.XMLHttpRequest();
            xhr.upload.addEventListener('progress', function(event) {
                if (event.lengthComputable) {
                    var pct = Math.round(event.loaded / event.total * 100);
                    $('#cloud-upload-fill').css('width', pct + '%');
                    $('#cloud-upload-text').text(pct + '%');
                }
            }, false);
            return xhr;
        }
    }).done(function() {
        $('#cloud-upload-status').text('上传成功');
        loadCloudFiles();
    }).fail(function() {
        $('#cloud-upload-status').text('上传失败，请重试');
    }).always(function() {
        $('#cloud-file-input').prop('disabled', false).val('');
        setTimeout(function() { $('#cloud-upload-progress').hide(); }, 800);
    });
}

var broadcastRoomSelection = new Set();

function cloudBroadcastControlsHTML() {
    return '<section class="settings-card"><h3>云端广播发布</h3><p class="settings-desc">这里编辑云端统一内容。保存后同步到所选机房，包含赛前 / 赛中 / 赛后页面、图片、字体、倒计时。推送时由中转打开本机房广播地址，屏幕流不上传云端。同步会替换所选机房的广播内容，不修改其局域网推送地址。</p><div class="ops-toolbar"><button class="btn btn-sm btn-outline" onclick="fleetRooms.forEach(function(room){broadcastRoomSelection.add(room.id);});renderBroadcastTargets()">选择全部机房</button><button class="btn btn-sm btn-outline" onclick="broadcastRoomSelection.clear();renderBroadcastTargets()">清空选择</button><button class="btn btn-primary" onclick="publishCloudBroadcast(\'sync\')">仅同步内容与素材</button></div><div id="bc-cloud-targets" class="ops-toolbar"></div><p id="bc-publish-status" role="status" class="settings-desc"></p></section>';
}

function renderBroadcastTargets() {
    if (!$('#bc-cloud-targets').length) return;
    $('#bc-cloud-targets').html(fleetRooms.map(function(room) {
        return '<label class="ops-check"><input type="checkbox" value="' + escapeHtml(room.id) + '" ' + (broadcastRoomSelection.has(room.id) ? 'checked' : '') + '> ' + escapeHtml(room.name) + (room.online ? ' · 在线' : ' · 离线，恢复后投递') + '</label>';
    }).join('') || '<p class="settings-desc">暂无机房连接，请先配置并机中转。</p>');
    $('#bc-cloud-targets input').on('change', function() { if (this.checked) broadcastRoomSelection.add(this.value); else broadcastRoomSelection.delete(this.value); });
}

function publishCloudBroadcast(action) {
    if (bcDirty) { showToast('请先保存广播修改，再进行同步或推送', 'error'); return; }
    var targets = Array.from(broadcastRoomSelection);
    if (!targets.length) { showToast('请明确选择目标机房', 'error'); return; }
    var labels = {sync:'同步内容与素材',start:'同步并推送当前模式',stop:'关闭广播',reset:'复位轮播时钟'};
    if (!confirm('将对 ' + targets.length + ' 个机房' + labels[action] + '。推送 / 关闭作用于机房内全部选手机；离线中转恢复后投递。是否继续？')) return;
    $('#bc-publish-status').text('正在生成发布快照与素材校验清单…');
    $.ajax({url:'/api/cluster/broadcast',method:'POST',contentType:'application/json',data:JSON.stringify({room_ids:targets,action:action,mode:bcMode})}).done(function(result) {
        $('#bc-publish-status').text('版本 ' + result.revision + ' 已入队，目标 ' + result.job_ids.length + ' 个机房。请查看投递回执及机房命令结果。');
        showToast('广播发布已入队', 'success');
        refreshFleetJobs();
    }).fail(function(request) { $('#bc-publish-status').text((request.responseJSON && request.responseJSON.error) || '发布失败'); });
}
