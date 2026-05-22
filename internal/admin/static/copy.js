"use strict";

class CopyBtn extends HTMLElement {
    connectedCallback() {
        var btn = document.createElement("button");
        btn.className = "copy-btn";
        if (this.hasAttribute("class")) {
            btn.className += " " + this.getAttribute("class");
        }
        btn.textContent = "Copy";
        btn.title = "Copy to clipboard";
        var self = this;
        btn.addEventListener("click", function (e) {
            e.preventDefault();
            e.stopPropagation();
            var text = self.copyText;
            if (!text) return;
            navigator.clipboard.writeText(text).then(function () {
                btn.textContent = "Copied!";
                btn.classList.add("copied");
                setTimeout(function () {
                    btn.textContent = "Copy";
                    btn.classList.remove("copied");
                }, 1500);
            });
        });
        this.prepend(btn);
    }

    get copyText() {
        var src = this.getAttribute("data-src");
        if (src) {
            var scope = this.closest(".code-block") || this.closest(".card") || document;
            var el = scope.querySelector(src);
            return el ? el.textContent : "";
        }

        var tpl = this.closest(".url-tpl");
        if (tpl) {
            var url = tpl.getAttribute("data-tpl");
            tpl.querySelectorAll(".tpl-select").forEach(function (sel) {
                url = url.replace("{" + sel.getAttribute("data-var") + "}", sel.value);
            });
            return url;
        }

        var inner = this.querySelector("[data-copy]");
        if (inner) return inner.getAttribute("data-copy");

        var textNodes = this.childNodes;
        var text = "";
        for (var i = 0; i < textNodes.length; i++) {
            if (textNodes[i].nodeType === 3) text += textNodes[i].textContent;
            else if (textNodes[i].tagName !== "BUTTON") text += textNodes[i].textContent;
        }
        return text.trim();
    }
}

customElements.define("copy-btn", CopyBtn);

document.addEventListener("DOMContentLoaded", function () {
    var ua = navigator.userAgent.toLowerCase();
    var defaultOS = "linux";
    var defaultArch = "amd64";

    if (ua.indexOf("mac") !== -1) defaultOS = "darwin";
    else if (ua.indexOf("win") !== -1) defaultOS = "windows";

    if (ua.indexOf("arm64") !== -1 || ua.indexOf("aarch64") !== -1) defaultArch = "arm64";

    document.querySelectorAll(".url-tpl").forEach(function (tplEl) {
        tplEl.querySelectorAll('.tpl-select[data-var="os"]').forEach(function (sel) {
            sel.value = defaultOS;
        });
        tplEl.querySelectorAll('.tpl-select[data-var="arch"]').forEach(function (sel) {
            sel.value = defaultArch;
        });
    });
});
