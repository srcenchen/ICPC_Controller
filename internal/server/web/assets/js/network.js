// Network blocking management page
"use strict";

var networkRules = [];

var RULE_TYPES = [
    {value: "DOMAIN-SUFFIX", label: "域名后缀"},
    {value: "DOMAIN-KEYWORD", label: "域名关键字"},
    {value: "DOMAIN", label: "完整域名"}
];

function loadNetwork() {
    $.when(
        $.getJSON("/api/devices"),
        $.getJSON("/api/network/rules")
    ).done(function(devicesResp, rulesResp) {
        allDevices = devicesResp[0];
        networkRules = rulesResp[0];
        renderNetworkPage();
    }).fail(function() {
        if (currentPage !== "network") return;
        $("#content").html('<div class="empty-state">加载失败</div>');
    });
}

function renderNetworkPage() {
    if (currentPage !== "network") return;
    var html = '' +
        '<div class="page-header"><h1>网络屏蔽管理</h1></div>' +

        // Rules section.
        '<div class="settings-card">' +
            '<h3>白名单规则</h3>' +
            '<p class="settings-desc">外网访问白名单：只有匹配以下规则的域名才能访问外网，其余全部拦截。支持 <code>DOMAIN-SUFFIX</code>（域名后缀）、<code>DOMAIN-KEYWORD</code>（关键字）、<code>DOMAIN</code>（完整域名）。</p>' +
            '<div id="rules-list"></div>' +
            '<div class="btn-row" style="margin-top:12px">' +
                '<button id="btn-add-rule" class="btn btn-outline">+ 添加规则</button>' +
                '</div>' +
            '<div id="rules-result" class="settings-result"></div>' +
        '</div>' +

        // Device selection.
        '<div class="settings-card">' +
            '<h3>目标设备</h3>' +
            '<p class="settings-desc">选择要应用/解除网络限制的选手机。</p>' +
            '<div id="device-selector-container"></div>' +
        '</div>' +

        // Action buttons.
        '<div class="settings-card">' +
            '<div class="btn-row">' +
                '<button id="btn-apply" class="btn btn-danger">应用网络限制</button>' +
                '<button id="btn-remove" class="btn btn-success">解除网络限制</button>' +
                '<span id="action-result" class="hint"></span>' +
            '</div>' +
        '</div>' +

        // Log area.
        '<div class="settings-card">' +
            '<h3>执行日志</h3>' +
            '<div id="network-log" class="command-output md">等待操作...</div>' +
        '</div>';

    $("#content").html(html);
    renderRules();
    renderDeviceList();
    bindNetworkEvents();
}

// ---- Rules rendering ----

function renderRules() {
    if (!networkRules.length) {
        $("#rules-list").html('<div class="empty-state" style="padding:24px">暂无规则，点击"+ 添加规则"创建</div>');
        return;
    }
    var html = '<div class="rules-editor">';
    html += '<div class="rule-header"><span>类型</span><span>值</span><span></span></div>';
    for (var i = 0; i < networkRules.length; i++) {
        var r = networkRules[i];
        html += '<div class="rule-row" data-index="' + i + '">' +
            renderTypeSelect(r.type) +
            '<input type="text" class="rule-value" value="' + escapeHtml(r.value) + '" placeholder="例如: baidu.com">' +
            '<div class="preset-actions">' +
                '<button class="btn-icon btn-delete" title="删除">&times;</button>' +
            '</div>' +
            '</div>';
    }
    html += '</div>';
    $("#rules-list").html(html);

    // Auto-save on input/select change (blur).
    $("#rules-list .rule-value, #rules-list .rule-type").on("blur", function() {
        autoSaveRules();
    });

    $("#rules-list .btn-delete").on("click", function() {
        var idx = $(this).closest(".rule-row").data("index");
        networkRules.splice(idx, 1);
        renderRules();
        autoSaveRules();
    });
}

function autoSaveRules() {
    var updated = [];
    $("#rules-list .rule-row").each(function() {
        var v = $(this).find(".rule-value").val().trim();
        if (v) {
            updated.push({
                type: $(this).find(".rule-type").val(),
                value: v
            });
        }
    });
    networkRules = updated;
    $.ajax({
        url: "/api/network/rules", method: "PUT", contentType: "application/json",
        data: JSON.stringify(updated),
        success: function() {
            showResult("rules-result", "已自动保存", "success");
        },
        error: function(xhr) { showResult("rules-result", parseError(xhr), "error"); }
    });
}

function renderTypeSelect(current) {
    var html = '<select class="rule-type">';
    for (var i = 0; i < RULE_TYPES.length; i++) {
        var t = RULE_TYPES[i];
        html += '<option value="' + t.value + '"' + (t.value === current ? ' selected' : '') + '>' + t.label + '</option>';
    }
    html += '</select>';
    return html;
}

// ---- Actions ----

function bindNetworkEvents() {
    $("#btn-add-rule").on("click", function() {
        networkRules.push({type: "DOMAIN-SUFFIX", value: ""});
        renderRules();
    });



    $("#btn-apply").on("click", function() {
        if (!confirm("确定要对选中设备应用网络限制吗？\n\n局域网和已配置的白名单域名可正常访问，其余外网将被屏蔽。")) return;
        doAction("/api/network/apply", "应用网络限制");
    });

    $("#btn-remove").on("click", function() {
        if (!confirm("确定要解除选中设备的网络限制吗？")) return;
        doAction("/api/network/remove", "解除网络限制");
    });
}

function doAction(url, label) {
    var isBroadcast = selectedTargets.length === 0;
    var body = {target_type: isBroadcast ? "broadcast" : "single"};
    if (!isBroadcast) body.target_id = selectedTargets[0]; // single mode takes first

    $("#network-log").text(label + "...");

    // If multi-select (but not broadcast), send one-by-one.
    if (!isBroadcast && selectedTargets.length > 1) {
        var remaining = selectedTargets.length;
        var logLines = [];
        selectedTargets.forEach(function(id) {
            $.ajax({
                url: url, method: "POST", contentType: "application/json",
                data: JSON.stringify({target_type: "single", target_id: id}),
                success: function(res) {
                    logLines.push("[设备 #" + id + "] 已派发 (ID:" + res.id + ")");
                    $("#network-log").text(logLines.join("\n"));
                    remaining--;
                    if (!remaining) $("#action-result").text("全部已派发").css("color", "var(--success)");
                },
                error: function(xhr) {
                    logLines.push("[设备 #" + id + "] 派发失败: " + parseError(xhr));
                    $("#network-log").text(logLines.join("\n"));
                    remaining--;
                }
            });
        });
        return;
    }

    // Broadcast or single.
    $.ajax({
        url: url, method: "POST", contentType: "application/json",
        data: JSON.stringify(body),
        success: function(res) {
            $("#network-log").text(label + " 已派发 (命令ID: " + res.id + ")\n请在'命令执行'页面查看执行结果。");
            $("#action-result").text("已派发 (ID:" + res.id + ")").css("color", "var(--success)");
            setTimeout(function() { $("#action-result").text(""); }, 5000);
        },
        error: function(xhr) {
            $("#network-log").text(label + " 失败: " + parseError(xhr));
            $("#action-result").text("派发失败").css("color", "var(--danger)");
        }
    });
}

// ---- Helpers ----

function showResult(id, message, type) {
    var el = $("#" + id);
    el.text(message).removeClass("success error").addClass(type).show();
    if (type === "success") setTimeout(function() { el.fadeOut(); }, 3000);
}

function parseError(xhr) {
    try { var err = JSON.parse(xhr.responseText); return err.error || "操作失败"; }
    catch(e) { return "操作失败"; }
}
