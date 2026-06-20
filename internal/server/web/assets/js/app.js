// ICPC Remote Control - App Core
"use strict";

var currentPage = "dashboard";
var adminWS = null;
var selectedTargets = []; // shared with commands.js — empty = broadcast

$(function() {
    $(".nav-link").on("click", function(e) {
        e.preventDefault();
        var page = $(this).data("page");
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
    var validPages = ["dashboard", "devices", "checkin", "commands", "network", "broadcast", "settings", "screen", "distribute"];
    var startPage = validPages.indexOf(hash) >= 0 ? hash : "dashboard";

    connectAdminWS();
    navigateTo(startPage);
});



function navigateTo(page) {
    currentPage = page;
    location.hash = page;
    $(".nav-link").removeClass("active");
    $('.nav-link[data-page="' + page + '"]').addClass("active");

    if (page !== "screen") {
        $("#screen-monitor-container img").attr("src", "about:blank");
        $("#screen-modal-overlay img").attr("src", "about:blank");
        $("#screen-modal-overlay").remove();
        if (typeof stopAllIOSLoops === "function") {
            stopAllIOSLoops();
        }
    }

    switch (page) {
        case "dashboard": loadDashboard(); break;
        case "devices":   loadDevices(); break;
        case "commands":  loadCommands(); break;
        case "network":   loadNetwork(); break;
        case "broadcast": loadBroadcastAdmin(); break;
        case "checkin":   renderCheckinPage(); break;
        case "settings":  loadSettings(); break;
        case "screen":    loadScreenMonitor(); break;
        case "distribute": loadDistribute(); break;
    }
}

function connectAdminWS() {
    var protocol = location.protocol === "https:" ? "wss:" : "ws:";
    var wsURL = protocol + "//" + location.host + "/ws/admin";
    adminWS = new WebSocket(wsURL);

    adminWS.onopen = function() {
        console.log("[admin-ws] 已连接");
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
        setTimeout(connectAdminWS, 3000);
    };

    adminWS.onerror = function(err) {
        console.error("[admin-ws] 错误:", err);
    };
}

function handleAdminEvent(msg) {
    switch (msg.event) {
        case "device_connected":
        case "device_disconnected":
        case "device_updated":
        case "checkin_updated":
            updateStatusBar();
            if (currentPage === "dashboard") loadDashboard();
            if (currentPage === "devices") loadDevices();
            if (currentPage === "checkin") loadCheckin();
            if (currentPage === "network") loadNetwork();
            if (currentPage === "screen" && typeof refreshScreenDevices === "function") refreshScreenDevices();
            // Update the device list on commands page if it's open.
            if (currentPage === "commands" && typeof allDevices !== "undefined") {
                $.getJSON("/api/devices", function(devices) {
                    allDevices = devices;
                    if ($("#device-selector-container").length) renderDeviceList();
                });
            }
            break;
        case "command_status":
            if (currentPage === "commands" && typeof updateCommandResult === "function") updateCommandResult(msg.data);
            if (currentPage === "dashboard") loadDashboard();
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
            break;
    }
}

function refreshCurrentPage() {
    updateStatusBar();
    navigateTo(currentPage);
}

function updateStatusBar() {
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
        pending: "等待中",
        dispatched: "已派发",
        running: "运行中",
        completed: "已完成",
        failed: "失败",
        timeout: "超时"
    };
    return map[status] || status;
}
