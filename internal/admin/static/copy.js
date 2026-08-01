"use strict";
(() => {
  // src/copy.ts
  var CopyBtn = class extends HTMLElement {
    connectedCallback() {
      const btn = document.createElement("button");
      btn.className = "copy-btn";
      if (this.hasAttribute("class")) {
        btn.className += " " + this.getAttribute("class");
      }
      btn.title = "Copy to clipboard";
      btn.addEventListener("click", (e) => {
        e.preventDefault();
        e.stopPropagation();
        const text = this.copyText;
        if (!text) return;
        navigator.clipboard.writeText(text).then(() => {
          btn.classList.add("copied");
          setTimeout(() => {
            btn.classList.remove("copied");
          }, 1500);
        });
      });
      this.appendChild(btn);
    }
    get copyText() {
      const src = this.getAttribute("data-src");
      if (src) {
        const scope = this.closest("td") || this.closest(".code-block") || this.closest(".card") || document;
        const el = scope.querySelector(src);
        if (!el) return "";
        return el.getAttribute("data-copy") || el.textContent || "";
      }
      let tpl = this.closest(".url-tpl");
      if (!tpl) {
        const scope = this.closest("td") || this.closest(".card") || document;
        tpl = scope.querySelector(".url-tpl");
      }
      if (tpl) {
        let url = tpl.getAttribute("data-tpl") || "";
        tpl.querySelectorAll(".tpl-select").forEach((sel) => {
          url = url.replace("{" + sel.getAttribute("data-var") + "}", sel.value);
        });
        return url;
      }
      const inner = this.querySelector("[data-copy]");
      if (inner) return inner.getAttribute("data-copy") || "";
      const childNodes = this.childNodes;
      let text = "";
      for (let i = 0; i < childNodes.length; i++) {
        const node = childNodes[i];
        if (node.nodeType === 3) text += node.textContent;
        else if (node.tagName !== "BUTTON") text += node.textContent;
      }
      return text.trim();
    }
  };
  customElements.define("copy-btn", CopyBtn);
  document.addEventListener("DOMContentLoaded", () => {
    const ua = navigator.userAgent.toLowerCase();
    let defaultOS = "linux";
    let defaultArch = "amd64";
    if (ua.indexOf("mac") !== -1) defaultOS = "darwin";
    else if (ua.indexOf("win") !== -1) defaultOS = "windows";
    if (ua.indexOf("arm64") !== -1 || ua.indexOf("aarch64") !== -1) defaultArch = "arm64";
    document.querySelectorAll(".url-tpl").forEach((tplEl) => {
      tplEl.querySelectorAll('.tpl-select[data-var="os"]').forEach((sel) => {
        sel.value = defaultOS;
      });
      tplEl.querySelectorAll('.tpl-select[data-var="arch"]').forEach((sel) => {
        sel.value = defaultArch;
      });
    });
  });
})();
