// ICPC Remote Control - App Core
"use strict";

var currentPage = "dashboard";
var adminWS = null;
var selectedTargets = []; // shared with commands.js — empty = broadcast

var PAGE_TITLES = {
    rooms: "机房总览",
    dashboard: "仪表盘", devices: "设备管理", screen: "屏幕监控",
    commands: "命令执行", network: "网络控制", distribute: "文件分发",
    power: "电源管理", checkin: "签到管理", broadcast: "赛场大屏",
    settings: "系统设置"
};

$(function() {
    initTheme();

    $(".nav-link").on("click", function(e) {
        e.preventDefault();
        var page = $(this).data("page");
        if (!page) return;
        navigateTo(page);
    });

    $("#btn-logout").on("click", function(e) {
        e.preventDefault();
        if (!confirm("确定要退出登录吗？")) return;
        $.ajax({
            url: "/api/auth/logout",
            method: "POST",
            success: function() {
                window.location.href = "/login.html";
            }
        });
    });

    // Set up global AJAX setup to handle 401 unauthorized errors
    $(document).ajaxError(function(event, jqXHR, ajaxSettings, thrownError) {
        if (jqXHR.status === 401) {
            window.location.href = "/login.html";
        }
    });

    // Restore last page from URL hash, or default to dashboard.
    var hash = location.hash.replace("#", "");
    var validPages = ["rooms", "dashboard", "devices", "checkin", "commands", "network", "broadcast", "settings", "screen", "distribute", "power"];
    var startPage = validPages.indexOf(hash) >= 0 ? hash : "dashboard";

    connectAdminWS();
    navigateTo(startPage);
    window.addEventListener("hashchange", function() {
        var page = location.hash.slice(1);
        if (page !== currentPage && PAGE_TITLES[page]) navigateTo(page);
    });

    // Esc closes any open modal overlay.
    $(document).on("keydown", function(e) {
        if (e.key === "Escape") {
            var $ov = $(".modal-overlay").last();
            if ($ov.length) {
                var $close = $ov.find(".modal-close").first();
                if ($close.length) { $close.trigger("click"); } else { $ov.remove(); }
            }
        }
    });
});

function initTheme() {
    var sun = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>';
    var moon = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8z"/></svg>';
    function apply(theme) {
        if (theme) document.documentElement.setAttribute("data-theme", theme);
        else document.documentElement.removeAttribute("data-theme");
        var isDark = document.documentElement.getAttribute("data-theme") === "dark";
        $("#theme-toggle").html(isDark ? sun : moon);
        if (typeof syncEditorThemes === "function") syncEditorThemes(isDark);
    }
    var stored = null;
    try { stored = localStorage.getItem("icpc-theme"); } catch (e) {}
    apply(stored);
    $("#theme-toggle").on("click", function() {
        var isDark = document.documentElement.getAttribute("data-theme") === "dark";
        var next = isDark ? "light" : "dark";
        try { localStorage.setItem("icpc-theme", next); } catch (e) {}
        apply(next);
    });
}

// timeAgo renders a relative timestamp; the absolute value goes in the title.
function timeAgo(iso) {
    if (!iso) return "-";
    var t = new Date(iso).getTime();
    if (isNaN(t)) return iso;
    var s = Math.floor((Date.now() - t) / 1000);
    if (s < 0) s = 0;
    if (s < 60) return s + " 秒前";
    if (s < 3600) return Math.floor(s / 60) + " 分钟前";
    if (s < 86400) return Math.floor(s / 3600) + " 小时前";
    return Math.floor(s / 86400) + " 天前";
}



function navigateTo(page) {
    if (currentPage === "broadcast" && page !== "broadcast" && typeof bcCanLeave === "function" && !bcCanLeave()) return;
    if (typeof guardCloudPage === "function") page = guardCloudPage(page);
    if (typeof cancelPendingPageRequests === "function") cancelPendingPageRequests();
    $("#content").html('<div class="empty-state" role="status">正在加载…</div>');
    if (page !== "broadcast" && window._bcClockTmr) clearInterval(window._bcClockTmr);
    $("#content").attr("data-page", page);
    currentPage = page;
    location.hash = page;
    $(".nav-link").removeClass("active");
    $('.nav-link[data-page="' + page + '"]').addClass("active");
    $("#topbar-title").text(PAGE_TITLES[page] || "ICPC 集控");

    if (page !== "screen") {
        $("#screen-monitor-container img").removeAttr("src");
        $("#screen-modal-overlay img").removeAttr("src");
        $("#screen-modal-overlay").remove();
        if (typeof stopAllIOSLoops === "function") {
            stopAllIOSLoops();
        }
    }

    if (typeof isCloudAllRooms === 'function' && isCloudAllRooms() && loadCloudPage(page)) return;
    switch (page) {
        case "rooms": loadRooms(); break;
        case "dashboard": loadDashboard(); break;
        case "devices":   loadDevices(); break;
        case "commands":  loadCommands(); break;
        case "network":   loadNetwork(); break;
        case "broadcast": loadBroadcastAdmin(); break;
        case "checkin":   renderCheckinPage(); break;
        case "settings":  loadSettings(); break;
        case "screen":    loadScreenMonitor(); break;
        case "distribute": loadDistribute(); break;
        case "power":     loadPower(); break;
    }
}

function connectAdminWS() {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var wsURL = protocol + "//" + location.host + "/ws/admin";
    adminWS = new WebSocket(wsURL);

    adminWS.onopen = function() {
        console.log("[admin-ws] 已连接");
        $("#conn-dot").removeClass("offline").addClass("online").text("实时");
        refreshCurrentPage();
    };

    adminWS.onmessage = function(event) {
        try {
            var msg = JSON.parse(event.data);
            handleAdminEvent(msg);
        } catch(e) {
            console.error("[admin-ws] 解析错误:", e);
        }
    };

    adminWS.onclose = function() {
        console.log("[admin-ws] 断开，3秒后重连");
        $("#conn-dot").removeClass("online").addClass("offline").text("重连中…");
        setTimeout(connectAdminWS, 3000);
    };

    adminWS.onerror = function(err) {
        console.error("[admin-ws] 错误:", err);
    };
}

var _deviceEventTimer = null;
function handleAdminEvent(msg) {
    if (msg.event === "snapshot_updated") {
        if (typeof loadSnapshotPanel === "function" && $("#snapshot-body").length) loadSnapshotPanel();
        return;
    }
    if (typeof isCloudAllRooms === 'function' && isCloudAllRooms()) return;
    switch (msg.event) {
        case "device_connected":
        case "device_disconnected":
        case "device_updated":
        case "checkin_updated":
            updateStatusBar();
            // Debounce full reloads; prefer partial row updates when available.
            clearTimeout(_deviceEventTimer);
            _deviceEventTimer = setTimeout(function() {
                if (currentPage === "dashboard" && typeof patchDashboardStats === "function") {
                    patchDashboardStats();
                } else if (currentPage === "dashboard") {
                    loadDashboard();
                }
                if (currentPage === "devices") {
                    if (typeof patchDevicesFromEvent === "function") {
                        patchDevicesFromEvent(msg);
                    } else {
                        loadDevices();
                    }
                }
                if (currentPage === "checkin") {
                    if (typeof patchCheckinFromEvent === "function") {
                        patchCheckinFromEvent(msg);
                    } else if (typeof loadCheckin === "function") {
                        loadCheckin();
                    }
                }
                // Network: never full-reload while editing rules.
                if (currentPage === "network" && typeof patchNetworkDevices === "function") {
                    patchNetworkDevices();
                }
                if (currentPage === "screen" && typeof refreshScreenDevices === "function") refreshScreenDevices();
                if (currentPage === "commands" && typeof allDevices !== "undefined") {
                    $.getJSON("/api/devices", function(devices) {
                        allDevices = devices;
                        if ($("#device-selector-container").length) renderDeviceList();
                    });
                }
                if (currentPage === "dashboard" && typeof patchDashboardTasks === "function") {
                    patchDashboardTasks();
                }
            }, 400);
            break;
        case "command_status":
            if (currentPage === "commands" && typeof updateCommandResult === "function") updateCommandResult(msg.data);
            if (currentPage === "dashboard" && typeof patchDashboardStats === "function") {
                patchDashboardStats();
            } else if (currentPage === "dashboard") {
                loadDashboard();
            }
            break;
        case "device_health":
            if (currentPage === "devices" && typeof patchDeviceHealth === "function") {
                patchDeviceHealth(msg.data);
            }
            break;
        case "command_output":
            if (typeof handleCommandOutput === "function") handleCommandOutput(msg.data);
            break;
        case "command_result":
            if (typeof handleCommandResult === "function") handleCommandResult(msg.data);
            break;
        case "distribute_progress_update":
        case "distribute_task_finished":
            if (currentPage === "distribute" && typeof handleDistributeEvent === "function") {
                handleDistributeEvent(msg.event, msg.data);
            }
            if (currentPage === "dashboard" && typeof patchDashboardTasks === "function") {
                patchDashboardTasks();
            }
            break;
    }
}

function refreshCurrentPage() {
    updateStatusBar();
    navigateTo(currentPage);
}

function updateStatusBar() {
    if (typeof deploymentConfig !== "undefined" && deploymentConfig.mode === "cloud" && !selectedRoom) { refreshRoomsData(); return; }
    $.getJSON("/api/stats", function(stats) {
        $("#online-count").text("在线: " + stats.online_devices);
        $("#total-count").text("总计: " + stats.total_devices);
    }).fail(function() {
        console.error("获取统计失败");
    });
}

function formatBytes(bytes) {
    if (bytes === 0) return "0 B";
    var units = ["B", "KB", "MB", "GB", "TB"];
    var i = Math.floor(Math.log(bytes) / Math.log(1024));
    return (bytes / Math.pow(1024, i)).toFixed(1) + " " + units[i];
}

function escapeHtml(str) {
    if (str === null || str === undefined) return "";
    return String(str)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
}

function statusLabel(status) {
    var map = {
        idle: "未运行", downloading: "下载中",
        pending: "等待中",
        dispatched: "已派发",
        running: "运行中",
        completed: "已完成",
        failed: "失败",
        timeout: "超时",
        stalled: "卡住",
        cancelled: "已取消"
        , verifying: "校验中", executing: "后置命令", stopped: "已停止", queued: "等待中转", sent: "已投递", unknown: "待核实"
    };
    return map[status] || status;
}

// Lightweight toast (replaces frequent alert for non-blocking feedback)
function showToast(message, type) {
    type = type || "info";
    var $host = $("#toast-host");
    if (!$host.length) {
        $("body").append('<div id="toast-host"></div>');
        $host = $("#toast-host");
    }
    var el = $('<div class="toast toast-' + type + '"></div>').text(message);
    $host.append(el);
    setTimeout(function() {
        el.addClass("toast-hide");
        setTimeout(function() { el.remove(); }, 300);
    }, 2800);
}
