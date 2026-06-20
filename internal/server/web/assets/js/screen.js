// Contestant Screen Page
"use strict";

var activeIOSLoops = [];
var largeIOSLoop = null;

function isIOS() {
    return /iPad|iPhone|iPod/.test(navigator.userAgent) || 
           (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
}

function stopAllIOSLoops() {
    activeIOSLoops.forEach(function(l) { l.stop(); });
    activeIOSLoops = [];
}

function startImageLoop(imgElement, ip, hd) {
    var active = true;
    
    function loadNext() {
        if (!active) return;
        var url = 'http://' + ip + ':8090/screen?hd=' + (hd ? '1' : '0') + '&single=1&_t=' + Date.now();
        
        var tempImg = new Image();
        tempImg.onload = function() {
            if (!active) return;
            imgElement.src = tempImg.src;
            setTimeout(loadNext, 250); // ~4 FPS
        };
        tempImg.onerror = function() {
            if (!active) return;
            setTimeout(loadNext, 1000);
        };
        tempImg.src = url;
    }
    
    loadNext();
    
    return {
        stop: function() {
            active = false;
        }
    };
}

function loadScreenMonitor() {
    stopAllIOSLoops();
    // Load settings first to check if screen capture is enabled.
    $.getJSON("/api/settings", function(settings) {
        renderScreenPage(settings);
    }).fail(function() {
        $("#content").html('<div class="empty-state">加载设置失败</div>');
    });
}

function renderScreenPage(settings) {
    var enabled = settings.screen_monitor_enabled || false;

    var html = 
        '<div class="screen-header">' +
            '<h2 class="section-title" style="margin:0;">选手屏幕</h2>' +
            '<div style="display:flex; align-items:center; gap:8px;">' +
                '<span style="font-weight:600; font-size:14px; color:var(--text-secondary);">是否开启屏幕捕捉:</span>' +
                '<label class="switch">' +
                    '<input type="checkbox" id="screen-capture-switch"' + (enabled ? ' checked' : '') + '>' +
                    '<span class="slider round"></span>' +
                '</label>' +
            '</div>' +
        '</div>' +
        '<div id="screen-monitor-container"></div>';

    $("#content").html(html);

    // Bind switch toggle event.
    $("#screen-capture-switch").on("change", function() {
        var checked = $(this).is(":checked");
        $.ajax({
            url: "/api/settings",
            method: "POST",
            contentType: "application/json",
            data: JSON.stringify({ screen_monitor_enabled: checked }),
            success: function(res) {
                // Re-render based on new state.
                loadScreenMonitor();
            },
            error: function(xhr) {
                alert("更新屏幕捕捉状态失败");
                $("#screen-capture-switch").prop("checked", !checked);
            }
        });
    });

    // Bind card click event delegation to show high-res screen modal.
    $("#screen-monitor-container").off("click").on("click", ".screen-card", function() {
        var assignedId = $(this).data("id");
        $.getJSON("/api/devices/" + assignedId, function(d) {
            var ip = getDeviceIP(d.local_ip);
            if (!ip || !d.connected) {
                alert("该设备已离线，无法开启高分屏幕监控");
                return;
            }
            var checkinLabel = d.student_name ? (d.student_name + ' (' + d.student_num + ')') : '未签到';
            showLargeScreen(d.assigned_id, d.hostname, ip, checkinLabel);
        }).fail(function() {
            alert("获取设备信息失败");
        });
    });

    if (enabled) {
        refreshScreenDevices();
    } else {
        $("#screen-monitor-container").html('<div class="empty-state" style="padding:48px;">屏幕捕捉功能未开启，请在右上方开启。</div>');
    }
}

function refreshScreenDevices() {
    $.getJSON("/api/devices", function(devices) {
        renderScreenDevices(devices);
    });
}

function renderScreenDevices(devices) {
    if (devices.length === 0) {
        $("#screen-monitor-container").html('<div class="empty-state">暂无已注册设备</div>');
        return;
    }

    // Sort devices by assigned ID.
    devices.sort(function(a, b) {
        return a.assigned_id - b.assigned_id;
    });

    var existingCards = {};
    $("#screen-monitor-container .screen-card").each(function() {
        var id = $(this).data("id");
        existingCards[id] = $(this);
    });

    var container = $("#screen-monitor-container");
    if (!container.find(".screen-grid").length) {
        container.html('<div class="screen-grid"></div>');
    }
    var grid = container.find(".screen-grid");

    devices.forEach(function(d) {
        var ip = getDeviceIP(d.local_ip) || "";
        var card = existingCards[d.assigned_id];
        var checkinLabel = d.student_name ? escapeHtml(d.student_name) + ' (' + escapeHtml(d.student_num) + ')' : '未签到';
        var statusBadge = '<span class="badge badge-' + (d.connected ? 'online' : 'offline') + '">' + (d.connected ? '在线' : '离线') + '</span>';

        var bodyHtml = '';
        if (!d.connected) {
            bodyHtml = '<div class="screen-placeholder">设备离线</div>';
        } else if (!ip) {
            bodyHtml = '<div class="screen-placeholder">未知IP</div>';
        } else {
            if (isIOS()) {
                bodyHtml = '<img class="ios-screen-img" data-ip="' + ip + '" data-hd="0" src="" onerror="this.style.display=\'none\'; $(this).siblings(\'.screen-err\').show();" />' +
                           '<div class="screen-placeholder screen-err" style="display:none;">无法连接屏幕流</div>';
            } else {
                var streamUrl = 'http://' + ip + ':8090/screen';
                bodyHtml = '<img src="' + streamUrl + '" onerror="this.style.display=\'none\'; $(this).siblings(\'.screen-err\').show();" />' +
                           '<div class="screen-placeholder screen-err" style="display:none;">无法连接屏幕流</div>';
            }
        }

        if (card) {
            // Card exists, update its header and content.
            card.find(".device-student").html(checkinLabel);
            card.find(".device-status").html(statusBadge);
            
            // Only update image/body if online state changed or card had offline placeholder.
            var wasOffline = card.find(".screen-placeholder:not(.screen-err)").length > 0;
            var isOffline = !d.connected;
            if (wasOffline !== isOffline) {
                card.find(".screen-card-body").html(bodyHtml);
            }
            delete existingCards[d.assigned_id];
        } else {
            // Create new card.
            var cardHtml = 
                '<div class="screen-card" data-id="' + d.assigned_id + '" style="cursor: pointer;">' +
                    '<div class="screen-card-header">' +
                        '<span><strong>#' + d.assigned_id + '</strong> (' + escapeHtml(d.hostname) + ')</span>' +
                        '<span class="device-student">' + checkinLabel + '</span>' +
                        '<span class="device-status">' + statusBadge + '</span>' +
                    '</div>' +
                    '<div class="screen-card-body">' +
                        bodyHtml +
                    '</div>' +
                '</div>';
            grid.append(cardHtml);
        }
    });

    // Remove cards of devices that no longer exist.
    for (var id in existingCards) {
        existingCards[id].remove();
    }

    if (isIOS()) {
        stopAllIOSLoops();
        $(".ios-screen-img").each(function() {
            var img = this;
            var ip = $(img).data("ip");
            var hd = $(img).data("hd") === 1;
            var loop = startImageLoop(img, ip, hd);
            activeIOSLoops.push(loop);
        });
    }
}

function getDeviceIP(localIpJson) {
    if (!localIpJson) return null;
    try {
        var ips = JSON.parse(localIpJson);
        if (Array.isArray(ips)) {
            // Sort by defaultRoute: true first
            ips.sort(function(a, b) {
                return (b.defaultRoute || false) - (a.defaultRoute || false);
            });
            for (var i = 0; i < ips.length; i++) {
                var ip = ips[i].ipv4;
                if (ip) {
                    ip = ip.split('/')[0];
                    if (ip !== "127.0.0.1" && !ip.startsWith("169.254")) {
                        return ip;
                    }
                }
            }
        }
    } catch(e) {}
    return null;
}

function showLargeScreen(assignedId, hostname, ip, checkinLabel) {
    if (!ip) return;
    
    var html = 
        '<div class="modal-overlay" id="screen-modal-overlay" onclick="closeLargeScreen(event)">' +
            '<div class="modal modal-large" onclick="event.stopPropagation()">' +
                '<button class="modal-close" onclick="closeLargeScreen()">&times;</button>' +
                '<h2>选手 #' + assignedId + ' (' + escapeHtml(hostname) + ') 屏幕监控 <small style="font-weight:normal;color:var(--text-secondary);font-size:14px;margin-left:10px;">' + escapeHtml(checkinLabel) + '</small></h2>' +
                '<div class="modal-large-body">';
                
    if (isIOS()) {
        html += '<img id="ios-large-img" src="" onerror="this.style.display=\'none\'; $(this).siblings(\'.screen-err\').show();" />';
    } else {
        var streamUrl = 'http://' + ip + ':8090/screen?hd=1';
        html += '<img src="' + streamUrl + '" onerror="this.style.display=\'none\'; $(this).siblings(\'.screen-err\').show();" />';
    }
    
    html += '<div class="screen-placeholder screen-err" style="display:none;position:relative;">无法连接高分辨率屏幕流</div>' +
                '</div>' +
            '</div>' +
        '</div>';
        
    $("body").append(html);
    
    if (isIOS()) {
        var imgEl = document.getElementById("ios-large-img");
        largeIOSLoop = startImageLoop(imgEl, ip, true);
    }
}

function closeLargeScreen(e) {
    if (e && e.target !== e.currentTarget) return;
    if (largeIOSLoop) {
        largeIOSLoop.stop();
        largeIOSLoop = null;
    }
    var overlay = $("#screen-modal-overlay");
    if (overlay.length) {
        overlay.find("img").attr("src", "about:blank");
        overlay.remove();
    }
}

