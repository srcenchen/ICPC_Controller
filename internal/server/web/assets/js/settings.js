// ICPC Remote Control - Settings Page
"use strict";

function loadSettings() {
    $.getJSON("/api/settings", function(s) {
        renderPage(s);
    }).fail(function() {
        if (currentPage !== "settings") return;
        $("#content").html('<div class="empty-state">加载设置失败</div>');
    });
}

function renderPage(settings) {
    if (currentPage !== "settings") return;
    var html =
        '<h2>系统设置</h2>' +

        // Hostname prefix section.
        '<div class="settings-card">' +
            '<h3>选手机主机名前缀</h3>' +
            '<p class="settings-desc">客户端注册后会被重命名为 <code>{前缀}-{编号}</code> 格式。修改后仅对新注册的客户端生效。</p>' +
            '<form id="prefix-form">' +
                '<div class="form-group">' +
                    '<input type="text" id="hostname-prefix" value="' + escapeHtml(settings.hostname_prefix) + '" placeholder="例如: cwxu-icpc" maxlength="64">' +
                    '<button type="submit" class="btn btn-primary">保存</button>' +
                '</div>' +
                '<div id="prefix-result" class="settings-result"></div>' +
            '</form>' +
            '<p class="settings-preview">预览：<strong id="prefix-preview">' + escapeHtml(settings.hostname_prefix) + '-1</strong></p>' +
        '</div>' +

        // Check-in config section.
        '<div class="settings-card">' +
            '<h3>签到页面配置</h3>' +
            '<p class="settings-desc">配置选手端签到页面（<code>:8090</code>）的显示内容和签退行为。修改后选手下次签到/签退时使用最新配置。</p>' +

            // Welcome text
            '<div class="cfg-section">' +
                '<div class="cfg-section-title">' +
                    '<span class="cfg-badge cfg-badge-primary">签到前</span> 欢迎语' +
                '</div>' +
                '<p class="cfg-hint">选手打开签到页时顶部显示的欢迎文字，蓝色大字居中。</p>' +
                '<textarea id="cfg-welcome" class="cfg-textarea" rows="2" placeholder="欢迎参加XCPC竞赛"></textarea>' +
            '</div>' +

            // Warning text
            '<div class="cfg-section">' +
                '<div class="cfg-section-title">' +
                    '<span class="cfg-badge cfg-badge-danger">签到前</span> 警告提示' +
                '</div>' +
                '<p class="cfg-hint">签到表单上方红色警告，提醒选手比赛纪律。</p>' +
                '<textarea id="cfg-warning" class="cfg-textarea" rows="2" placeholder="严禁场外答题，否则成绩无效！"></textarea>' +
            '</div>' +

            // Post-checkin message
            '<div class="cfg-section">' +
                '<div class="cfg-section-title">' +
                    '<span class="cfg-badge cfg-badge-success">签到后</span> 成功提示语' +
                '</div>' +
                '<p class="cfg-hint">签到成功后弹窗显示的文字。</p>' +
                '<input type="text" id="cfg-post-checkin-msg" class="cfg-input" placeholder="签到成功">' +
            '</div>' +

            // Post-checkout
            '<div class="cfg-section">' +
                '<div class="cfg-section-title">' +
                    '<span class="cfg-badge cfg-badge-warning">签退后</span> 执行命令' +
                '</div>' +
                '<p class="cfg-hint">签退成功后执行的 Shell 命令。为空则不执行。建议：<code>shutdown -h +1</code>（1分钟后关机）。</p>' +
                '<textarea id="cfg-post-checkout-cmd" class="cfg-textarea cfg-textarea-mono" rows="2" placeholder="shutdown -h +1"></textarea>' +
            '</div>' +

            '<div class="cfg-section">' +
                '<div class="cfg-section-title">' +
                    '<span class="cfg-badge cfg-badge-warning">签退后</span> 提示语' +
                '</div>' +
                '<p class="cfg-hint">签退成功后弹窗显示的文字，同时显示在"已签退"页面底部。</p>' +
                '<textarea id="cfg-post-checkout-msg" class="cfg-textarea" rows="2" placeholder="签退成功，您的电脑将在1分钟后自动关机。"></textarea>' +
            '</div>' +

            '<div style="margin-top:16px; display:flex; gap:8px;">' +
                '<button id="btn-save-checkin-cfg" class="btn btn-primary">保存签到配置</button>' +
                '<button id="btn-reset-checkin-cfg" class="btn btn-outline">恢复默认</button>' +
            '</div>' +
            '<div id="checkin-cfg-result" class="settings-result"></div>' +
        '</div>' +

        // Presets section.
        '<div class="settings-card">' +
            '<h3>预设命令管理</h3>' +
            '<p class="settings-desc">在"命令执行"页面显示的快捷命令。拖拽排序，编辑后点击保存。</p>' +
            '<div id="presets-list"></div>' +
            '<div style="margin-top:12px; display:flex; gap:8px;">' +
                '<button id="btn-add-preset" class="btn btn-outline">+ 添加命令</button>' +
                '<button id="btn-save-presets" class="btn btn-primary">保存预设</button>' +
            '</div>' +
            '<div id="presets-result" class="settings-result"></div>' +
        '</div>' +

        // Maintenance / retention section
        '<div class="settings-card">' +
            '<h3>运维与保留策略</h3>' +
            '<p class="settings-desc">命令日志与设备事件的自动清理，以及健康告警阈值。</p>' +
            '<div class="power-form" style="margin-bottom:8px">' +
                '<label>命令日志保留(天)<input type="number" id="mt-cmd-days" min="0" style="width:90px"></label>' +
                '<label>命令日志上限(条)<input type="number" id="mt-cmd-max" min="0" style="width:110px"></label>' +
                '<label>事件保留(天)<input type="number" id="mt-event-days" min="0" style="width:90px"></label>' +
            '</div>' +
            '<div class="power-form" style="margin-bottom:12px">' +
                '<label>磁盘告警(%)<input type="number" id="mt-disk" min="0" max="100" style="width:80px"></label>' +
                '<label>温度告警(°C)<input type="number" id="mt-temp" min="0" style="width:80px"></label>' +
                '<label>内存告警(%)<input type="number" id="mt-mem" min="0" max="100" style="width:80px"></label>' +
                '<button id="btn-save-maint" class="btn btn-primary">保存</button>' +
            '</div>' +
            '<div id="maint-result" class="settings-result"></div>' +
        '</div>' +

        // Client update section
        '<div class="settings-card">' +
            '<h3>客户端更新</h3>' +
            '<p class="settings-desc">在“分发”页上传新客户端二进制（文件名 icpc-client）后，推送在线选手机自更新。</p>' +
            '<button id="btn-update-clients" class="btn btn-warning">推送客户端更新</button>' +
            '<div id="update-result" class="settings-result"></div>' +
        '</div>' +

        // Change password section
        '<div class="settings-card">' +
            '<h3>修改管理员密码</h3>' +
            '<p class="settings-desc">修改用于登录后台的管理员密码。</p>' +
            '<form id="password-form" style="max-width:320px;">' +
                '<div class="form-group" style="margin-bottom:12px; display:flex; flex-direction:column; gap:4px; align-items:stretch;">' +
                    '<label style="font-weight:600; font-size:13px; color:var(--text-secondary);">旧密码</label>' +
                    '<input type="password" id="pwd-old" placeholder="请输入旧密码">' +
                '</div>' +
                '<div class="form-group" style="margin-bottom:16px; display:flex; flex-direction:column; gap:4px; align-items:stretch;">' +
                    '<label style="font-weight:600; font-size:13px; color:var(--text-secondary);">新密码</label>' +
                    '<input type="password" id="pwd-new" placeholder="请输入新密码">' +
                '</div>' +
                '<button type="submit" class="btn btn-primary">修改密码</button>' +
                '<div id="password-result" class="settings-result"></div>' +
            '</form>' +
        '</div>';

    $("#content").html(html);
	if (typeof deploymentSettingsHTML === "function") {
		$("#content > h2").after(deploymentSettingsHTML(settings.deployment));
		bindDeploymentSettings(settings.deployment);
	}

    // Hostname prefix.
    $("#hostname-prefix").on("input", function() {
        var val = $(this).val().trim() || "?";
        $("#prefix-preview").text(val + "-1");
    });
    $("#prefix-form").on("submit", function(e) {
        e.preventDefault();
        var prefix = $("#hostname-prefix").val().trim();
        if (!prefix) { showResult("prefix-result", "前缀不能为空", "error"); return; }
        $.ajax({
            url: "/api/settings", method: "POST", contentType: "application/json",
            data: JSON.stringify({hostname_prefix: prefix}),
            success: function(res) {
                showResult("prefix-result", "已保存", "success");
                $("#prefix-preview").text(res.hostname_prefix + "-1");
            },
            error: function(xhr) { showResult("prefix-result", parseError(xhr), "error"); }
        });
    });

    // Check-in config.
    var cfg = settings.checkin_config || {};
    var defaults = {
        welcome_text:     "欢迎参加XCPC竞赛",
        warning_text:     "严禁场外答题，否则成绩无效！",
        post_checkin_msg: "签到成功",
        post_checkout_cmd: "shutdown -h +1",
        post_checkout_msg: "签退成功，您的电脑将在1分钟后自动关机。"
    };
    $("#cfg-welcome").val(cfg.welcome_text || '');
    $("#cfg-warning").val(cfg.warning_text || '');
    $("#cfg-post-checkin-msg").val(cfg.post_checkin_msg || '');
    $("#cfg-post-checkout-cmd").val(cfg.post_checkout_cmd || '');
    $("#cfg-post-checkout-msg").val(cfg.post_checkout_msg || '');
    $("#btn-save-checkin-cfg").on("click", function() {
        var data = {
            welcome_text:     $("#cfg-welcome").val().trim(),
            warning_text:     $("#cfg-warning").val().trim(),
            post_checkin_msg: $("#cfg-post-checkin-msg").val().trim(),
            post_checkout_cmd: $("#cfg-post-checkout-cmd").val().trim(),
            post_checkout_msg: $("#cfg-post-checkout-msg").val().trim()
        };
        $.ajax({
            url: "/api/settings/checkin", method: "PUT", contentType: "application/json",
            data: JSON.stringify(data),
            success: function() { showResult("checkin-cfg-result", "签到配置已保存", "success"); },
            error: function(xhr) { showResult("checkin-cfg-result", parseError(xhr), "error"); }
        });
    });
    $("#btn-reset-checkin-cfg").on("click", function() {
        $("#cfg-welcome").val(defaults.welcome_text);
        $("#cfg-warning").val(defaults.warning_text);
        $("#cfg-post-checkin-msg").val(defaults.post_checkin_msg);
        $("#cfg-post-checkout-cmd").val(defaults.post_checkout_cmd);
        $("#cfg-post-checkout-msg").val(defaults.post_checkout_msg);
        showResult("checkin-cfg-result", "已恢复默认值，请点击保存", "success");
    });

    // Presets.
    var presets = settings.presets || [];
    renderPresets(presets);

    $("#btn-add-preset").on("click", function() {
        presets.push({name: "", desc: "", command: "", color: "primary"});
        renderPresets(presets);
    });

    $("#btn-save-presets").on("click", function() {
        // Read current values from DOM.
        var updated = [];
        $("#presets-list .preset-row").each(function() {
            updated.push({
                name:    $(this).find(".preset-name").val().trim(),
                desc:    $(this).find(".preset-desc").val().trim(),
                command: $(this).find(".preset-cmd").val().trim(),
                color:   $(this).find(".preset-color").val()
            });
        });

        // Validate.
        for (var i = 0; i < updated.length; i++) {
            if (!updated[i].name) { showResult("presets-result", "命令名称不能为空", "error"); return; }
            if (!updated[i].command) { showResult("presets-result", "命令内容不能为空", "error"); return; }
        }

        $.ajax({
            url: "/api/settings/presets", method: "PUT", contentType: "application/json",
            data: JSON.stringify(updated),
            success: function(res) {
                presets = res;
                renderPresets(presets);
                showResult("presets-result", "预设已保存", "success");
            },
            error: function(xhr) { showResult("presets-result", parseError(xhr), "error"); }
        });
    });

    // Maintenance / retention.
    var mt = settings.maintenance || {};
    $("#mt-cmd-days").val(mt.cmd_retention_days != null ? mt.cmd_retention_days : 14);
    $("#mt-cmd-max").val(mt.cmd_retention_max != null ? mt.cmd_retention_max : 5000);
    $("#mt-event-days").val(mt.event_retention_day != null ? mt.event_retention_day : 30);
    $("#mt-disk").val(mt.disk_alert_pct != null ? mt.disk_alert_pct : 90);
    $("#mt-temp").val(mt.temp_alert_c != null ? mt.temp_alert_c : 85);
    $("#mt-mem").val(mt.mem_alert_pct != null ? mt.mem_alert_pct : 92);
    $("#btn-save-maint").on("click", function() {
        var data = { maintenance: {
            cmd_retention_days:  parseInt($("#mt-cmd-days").val(), 10) || 0,
            cmd_retention_max:   parseInt($("#mt-cmd-max").val(), 10) || 0,
            event_retention_day: parseInt($("#mt-event-days").val(), 10) || 0,
            disk_alert_pct:      parseFloat($("#mt-disk").val()) || 0,
            temp_alert_c:        parseFloat($("#mt-temp").val()) || 0,
            mem_alert_pct:       parseFloat($("#mt-mem").val()) || 0
        }};
        $.ajax({
            url: "/api/settings", method: "POST", contentType: "application/json",
            data: JSON.stringify(data),
            success: function() { showResult("maint-result", "已保存", "success"); },
            error: function(xhr) { showResult("maint-result", parseError(xhr), "error"); }
        });
    });

    // Client update push.
    $("#btn-update-clients").on("click", function() {
        if (!confirm("将向所有在线选手机推送客户端自更新，确定继续？")) return;
        $.ajax({
            url: "/api/client/update", method: "POST",
            success: function(res) { showResult("update-result", "已推送到 " + res.sent + " 台在线设备", "success"); },
            error: function(xhr) { showResult("update-result", parseError(xhr), "error"); }
        });
    });

    // Password form.
    $("#password-form").on("submit", function(e) {
        e.preventDefault();
        var oldPwd = $("#pwd-old").val();
        var newPwd = $("#pwd-new").val();
        if (!oldPwd || !newPwd) { showResult("password-result", "旧密码和新密码不能为空", "error"); return; }
        $.ajax({
            url: "/api/auth/password", method: "POST", contentType: "application/json",
            data: JSON.stringify({old_password: oldPwd, new_password: newPwd}),
            success: function() {
                showResult("password-result", "密码修改成功", "success");
                $("#pwd-old").val("");
                $("#pwd-new").val("");
            },
            error: function(xhr) { showResult("password-result", parseError(xhr), "error"); }
        });
    });
}

// ---- Presets List Rendering ----

var COLOR_OPTIONS = [
    {value: "primary",  label: "蓝"},
    {value: "success",  label: "绿"},
    {value: "warning",  label: "橙"},
    {value: "danger",   label: "红"},
    {value: "info",     label: "青"},
    {value: "dark",     label: "灰"}
];

function renderPresets(presets) {
    if (!presets.length) {
        $("#presets-list").html('<div class="empty-state" style="padding:24px">暂无预设命令，点击"+ 添加命令"创建</div>');
        return;
    }

    var html = '<div class="presets-table">';
    html += '<div class="preset-header">' +
        '<span>名称</span><span>描述</span><span>命令</span><span>颜色</span><span></span>' +
        '</div>';

    for (var i = 0; i < presets.length; i++) {
        var p = presets[i];
        html += '<div class="preset-row" data-index="' + i + '">' +
            '<input type="text" class="preset-name" value="' + escapeHtml(p.name) + '" placeholder="名称">' +
            '<input type="text" class="preset-desc" value="' + escapeHtml(p.desc || "") + '" placeholder="描述">' +
            '<input type="text" class="preset-cmd" value="' + escapeHtml(p.command) + '" placeholder="shell 命令">' +
            renderColorSelect(p.color || "primary") +
            '<div class="preset-actions">' +
                '<button class="btn-icon btn-move-up" title="上移">&#9650;</button>' +
                '<button class="btn-icon btn-move-down" title="下移">&#9660;</button>' +
                '<button class="btn-icon btn-delete" title="删除">&times;</button>' +
            '</div>' +
            '</div>';
    }
    html += '</div>';
    $("#presets-list").html(html);

    // Color select change.
    $("#presets-list .preset-color").on("change", function() {
        // No action needed; values read on save.
    });

    // Delete.
    $("#presets-list .btn-delete").on("click", function() {
        var idx = $(this).closest(".preset-row").data("index");
        presets.splice(idx, 1);
        renderPresets(presets);
    });

    // Move up.
    $("#presets-list .btn-move-up").on("click", function() {
        var idx = $(this).closest(".preset-row").data("index");
        if (idx > 0) {
            var tmp = presets[idx-1]; presets[idx-1] = presets[idx]; presets[idx] = tmp;
            renderPresets(presets);
        }
    });

    // Move down.
    $("#presets-list .btn-move-down").on("click", function() {
        var idx = $(this).closest(".preset-row").data("index");
        if (idx < presets.length - 1) {
            var tmp = presets[idx+1]; presets[idx+1] = presets[idx]; presets[idx] = tmp;
            renderPresets(presets);
        }
    });
}

function renderColorSelect(current) {
    var html = '<select class="preset-color">';
    for (var i = 0; i < COLOR_OPTIONS.length; i++) {
        var c = COLOR_OPTIONS[i];
        html += '<option value="' + c.value + '"' + (c.value === current ? ' selected' : '') + '>' + c.label + '</option>';
    }
    html += '</select>';
    return html;
}

// ---- Helpers ----

function showResult(id, message, type) {
    var el = $("#" + id);
    el.text(message).removeClass("success error").addClass(type).show();
    if (type === "success") {
        setTimeout(function() { el.fadeOut(); }, 3000);
    }
}

function parseError(xhr) {
    try { var err = JSON.parse(xhr.responseText); return err.error || "操作失败"; }
    catch(e) { return "操作失败"; }
}
