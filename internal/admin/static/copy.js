"use strict";
document.addEventListener("DOMContentLoaded", function () {
    var ua = navigator.userAgent.toLowerCase();
    var defaultOS = "linux";
    var defaultArch = "amd64";

    if (ua.indexOf("mac") !== -1) {
        defaultOS = "darwin";
    } else if (ua.indexOf("win") !== -1) {
        defaultOS = "windows";
    }

    if (ua.indexOf("arm64") !== -1 || ua.indexOf("aarch64") !== -1) {
        defaultArch = "arm64";
    }

    function resolveURL(tplEl) {
        var tpl = tplEl.getAttribute("data-tpl");
        tplEl.querySelectorAll(".tpl-select").forEach(function (sel) {
            tpl = tpl.replace("{" + sel.getAttribute("data-var") + "}", sel.value);
        });
        return tpl;
    }

    function addCopyBtn(parent, getText) {
        var btn = document.createElement("button");
        btn.className = "copy-btn";
        btn.title = "Copy to clipboard";
        btn.textContent = "Copy";
        btn.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopPropagation();
            navigator.clipboard.writeText(getText()).then(function () {
                btn.textContent = "Copied!";
                btn.classList.add("copied");
                setTimeout(function () {
                    btn.textContent = "Copy";
                    btn.classList.remove("copied");
                }, 1500);
            });
        });
        parent.appendChild(btn);
    }

    document.querySelectorAll(".url-tpl").forEach(function (tplEl) {
        tplEl.querySelectorAll('.tpl-select[data-var="os"]').forEach(function (sel) {
            sel.value = defaultOS;
        });
        tplEl.querySelectorAll('.tpl-select[data-var="arch"]').forEach(function (sel) {
            sel.value = defaultArch;
        });

        addCopyBtn(tplEl, function () { return resolveURL(tplEl); });
    });

    document.querySelectorAll(".copyable").forEach(function (el) {
        var cell = el.closest(".endpoint-cell") || el.parentNode;
        addCopyBtn(cell, function () {
            return el.getAttribute("data-copy") || el.textContent;
        });
    });

    document.querySelectorAll(".code-block").forEach(function (block) {
        var pre = block.querySelector("pre");
        if (!pre) return;
        var label = block.querySelector(".code-label");
        if (!label) return;
        label.style.position = "relative";
        var btn = document.createElement("button");
        btn.className = "copy-btn code-copy-btn";
        btn.title = "Copy to clipboard";
        btn.textContent = "Copy";
        btn.addEventListener("click", function () {
            navigator.clipboard.writeText(pre.textContent).then(function () {
                btn.textContent = "Copied!";
                btn.classList.add("copied");
                setTimeout(function () {
                    btn.textContent = "Copy";
                    btn.classList.remove("copied");
                }, 1500);
            });
        });
        label.appendChild(btn);
    });
});
