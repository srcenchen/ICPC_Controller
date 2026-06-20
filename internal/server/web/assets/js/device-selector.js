// Shared device selection component — used by commands.js and network.js.
// Depends on globals from app.js: selectedTargets, escapeHtml.
"use strict";

var allDevices = [];
var deviceFilter = "";

function parseDeviceIP(localIP) {
    if (!localIP) return '';
    try {
        var ips = JSON.parse(localIP);
        if (Array.isArray(ips) && ips.length > 0) {
            for (var i = 0; i < ips.length; i++) {
                if (ips[i].defaultRoute && ips[i].ipv4) {
                    return ips[i].ipv4.split('/')[0];
                }
            }
            for (var j = 0; j < ips.length; j++) {
                if (ips[j].ipv4) {
                    return ips[j].ipv4.split('/')[0];
                }
            }
        }
    } catch(e) { 
        return localIP.split('/')[0]; 
    }
    return '';
}

function filteredDevices() {
    var q = deviceFilter.toLowerCase().trim();
    if (!q) return allDevices;
    return allDevices.filter(function(d) {
        var ip = parseDeviceIP(d.local_ip);
        return (String(d.assigned_id).indexOf(q) >= 0) ||
               ((d.hostname || '').toLowerCase().indexOf(q) >= 0) ||
               (ip.toLowerCase().indexOf(q) >= 0) ||
               ((d.student_name || '').toLowerCase().indexOf(q) >= 0) ||
               ((d.student_num || '').toLowerCase().indexOf(q) >= 0);
    });
}

function renderDeviceList() {
    var onlineCount = allDevices.filter(function(d) { return d.connected; }).length;
    var selCount = selectedTargets.length;

    // Save search focus/cursor before re-render.
    var searchEl = document.getElementById("device-search");
    var cursorPos = searchEl ? searchEl.selectionStart : 0;
    var wasFocused = searchEl === document.activeElement;

    var html = '';
    // Search + toolbar
    html += '<input class="device-search" id="device-search" type="text" placeholder="搜索设备 (ID/主机名/IP/姓名/学号)..." value="' + escapeHtml(deviceFilter) + '">';
    html += '<div class="device-toolbar">';
    html += '<button class="btn btn-sm" onclick="selectAllOnline()">全选在线 (' + onlineCount + ')</button>';
    html += '<button class="btn btn-sm" onclick="selectAllDevices()">全选 (' + allDevices.length + ')</button>';
    html += '<button class="btn btn-sm" onclick="deselectAllDevices()">取消选择</button>';
    html += '<span id="device-sel-count" style="font-size:12px;color:var(--accent);padding:4px;' + (selCount > 0 ? '' : 'display:none;') + '">已选 ' + selCount + ' 台</span>';
    html += '</div>';

    // Device list
    var devices = filteredDevices();
    html += '<div class="device-list" id="device-list">';

    // Broadcast row
    var broadcastSelected = selectedTargets.length === 0;
    html += '<div class="device-list-item broadcast' + (broadcastSelected ? ' selected' : '') + '" data-device-id="broadcast">';
    html += '<span class="device-list-check">' + (broadcastSelected ? '☑' : '☐') + '</span>';
    html += '<span class="device-list-id" style="width:auto;flex:1;">全部设备（广播）</span>';
    html += '<span class="device-list-ip" style="max-width:none;font-weight:500;">' + onlineCount + '/' + allDevices.length + ' 在线</span>';
    html += '</div>';

    for (var i = 0; i < devices.length; i++) {
        var d = devices[i];
        var sel = selectedTargets.indexOf(d.assigned_id) >= 0;
        var ip = parseDeviceIP(d.local_ip);
        var studentStr = d.student_name ? (' | ' + escapeHtml(d.student_name) + ' ' + escapeHtml(d.student_num)) : '';
        html += '<div class="device-list-item' + (sel ? ' selected' : '') + (d.connected ? '' : ' offline') + '" data-device-id="' + d.assigned_id + '">';
        html += '<span class="device-list-check">' + (sel ? '☑' : '☐') + '</span>';
        html += '<span class="device-list-id">#' + d.assigned_id + '</span>';
        html += '<span class="device-list-name">' + escapeHtml(d.hostname || d.username || '-') + '<span style="color:var(--accent);font-size:11px;">' + studentStr + '</span></span>';
        html += '<span class="device-list-ip">' + (ip || '-') + '</span>';
        html += '<span class="device-list-dot' + (d.connected ? ' online' : '') + '"></span>';
        html += '</div>';
    }
    html += '</div>';

    $("#device-selector-container").html(html);

    // Restore search focus/cursor.
    var newSearchEl = document.getElementById("device-search");
    if (newSearchEl && wasFocused) {
        newSearchEl.focus();
        newSearchEl.setSelectionRange(cursorPos, cursorPos);
    }

    // Use event delegation — clicks handled once, no per-row onclick in HTML.
    $("#device-list").off("click").on("click", ".device-list-item", function() {
        var id = $(this).data("device-id");
        if (id === "broadcast") {
            toggleBroadcast();
        } else {
            toggleSelectDevice(id);
        }
    });

    // Search: full re-render only when filter text changes.
    $("#device-search").off("input").on("input", function() {
        deviceFilter = $(this).val();
        renderDeviceList();
    });
}

// toggleSelectDevice: direct DOM manipulation, NO full re-render.
function toggleSelectDevice(id) {
    var idx = selectedTargets.indexOf(id);
    if (idx >= 0) {
        selectedTargets.splice(idx, 1);
    } else {
        selectedTargets.push(id);
    }
    updateDeviceSelectionUI();
}

// toggleBroadcast: clear selections, update broadcast row, full re-render not needed.
function toggleBroadcast() {
    if (selectedTargets.length === 0) return; // already broadcast
    selectedTargets = [];
    updateDeviceSelectionUI();
}

// updateDeviceSelectionUI: fast DOM-only updates after selection changes.
function updateDeviceSelectionUI() {
    var selCount = selectedTargets.length;
    var broadcastSelected = selCount === 0;

    // Update broadcast row.
    var broadcastRow = $('#device-list .device-list-item[data-device-id="broadcast"]');
    broadcastRow.toggleClass('selected', broadcastSelected);
    broadcastRow.find('.device-list-check').text(broadcastSelected ? '☑' : '☐');

    // Update each device row.
    $('#device-list .device-list-item[data-device-id]').each(function() {
        var id = $(this).data('device-id');
        if (id === 'broadcast') return;
        var sel = selectedTargets.indexOf(id) >= 0;
        $(this).toggleClass('selected', sel);
        $(this).find('.device-list-check').text(sel ? '☑' : '☐');
    });

    // Update counter.
    var counterEl = $('#device-sel-count');
    if (selCount > 0) {
        counterEl.text('已选 ' + selCount + ' 台').show();
    } else {
        counterEl.hide();
    }
}

// These full re-renders are only for batch operations (filter change is handled
// separately in renderDeviceList via the search input event).
function selectAllOnline() {
    selectedTargets = [];
    allDevices.forEach(function(d) { if (d.connected) selectedTargets.push(d.assigned_id); });
    renderDeviceList();
}

function selectAllDevices() {
    selectedTargets = allDevices.map(function(d) { return d.assigned_id; });
    renderDeviceList();
}

function deselectAllDevices() {
    selectedTargets = [];
    renderDeviceList();
}
