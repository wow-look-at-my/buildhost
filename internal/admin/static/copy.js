"use strict";
document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll(".copyable").forEach(function (el) {
        var btn = document.createElement("button");
        btn.className = "copy-btn";
        btn.title = "Copy to clipboard";
        btn.textContent = "Copy";
        btn.addEventListener("click", function () {
            var text = el.getAttribute("data-copy") || el.textContent;
            navigator.clipboard.writeText(text).then(function () {
                btn.textContent = "Copied!";
                btn.classList.add("copied");
                setTimeout(function () {
                    btn.textContent = "Copy";
                    btn.classList.remove("copied");
                }, 1500);
            });
        });
        el.parentNode.insertBefore(btn, el.nextSibling);
    });

    document.querySelectorAll(".code-block").forEach(function (block) {
        var pre = block.querySelector("pre");
        if (!pre) return;
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
        var label = block.querySelector(".code-label");
        if (label) {
            label.style.position = "relative";
            label.appendChild(btn);
        }
    });
});
