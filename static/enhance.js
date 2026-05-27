// Progressive enhancement for media posts. The page is fully functional
// without this script; it upgrades image thumbnails to inline expand/collapse
// and SWF thumbnails to inline Ruffle playback. If it fails to load, links
// fall back to their href (open image in new tab / download the .swf).
(function () {
    "use strict";

    // Left-click only; let ctrl/cmd/shift/middle-click do the browser default
    // (open in new tab), so the href fallback stays useful even when enhanced.
    function isPlainClick(e) {
        return e.button === 0 && !e.ctrlKey && !e.metaKey && !e.shiftKey && !e.altKey;
    }

    // a.expandable: toggle between the thumbnail and the full image inline.
    function enhanceImages() {
        document.querySelectorAll("a.expandable").forEach(function (link) {
            var img = link.querySelector("img");
            if (!img) return;
            var thumbSrc = img.src;
            var thumbW = img.getAttribute("width");
            var thumbH = img.getAttribute("height");
            var fullSrc = link.dataset.full;
            var expanded = false;

            link.addEventListener("click", function (e) {
                if (!isPlainClick(e)) return;
                e.preventDefault();
                expanded = !expanded;
                if (expanded) {
                    img.src = fullSrc;
                    img.removeAttribute("width");
                    img.removeAttribute("height");
                    img.classList.add("expanded");
                } else {
                    img.src = thumbSrc;
                    if (thumbW) img.setAttribute("width", thumbW);
                    if (thumbH) img.setAttribute("height", thumbH);
                    img.classList.remove("expanded");
                }
            });
        });
    }

    // a.swf-embed: replace the thumbnail with an inline Ruffle player on click.
    function enhanceFlash() {
        document.querySelectorAll("a.swf-embed").forEach(function (link) {
            link.addEventListener("click", function (e) {
                // Ruffle missing -> let the browser follow href and download the .swf.
                if (!window.RufflePlayer) return;
                if (!isPlainClick(e)) return;
                e.preventDefault();

                var img = link.querySelector("img");
                var player = window.RufflePlayer.newest().createPlayer();
                if (img) {
                    player.style.width = img.width + "px";
                    player.style.height = img.height + "px";
                }
                link.replaceWith(player);
                player.load({ url: link.dataset.swf, autoplay: "on" });
            });
        });
    }

    // a.video-embed: replace the thumbnail with an inline <video> on click; a
    // [最小化] link swaps the original thumbnail back. Keeping the collapsed state
    // as the thumbnail <img> avoids browsers rendering a cramped control bar on a
    // shrunk video (notably Safari).
    function enhanceVideos() {
        document.querySelectorAll("a.video-embed").forEach(function (link) {
            link.addEventListener("click", function (e) {
                if (!isPlainClick(e)) return;
                e.preventDefault();

                var video = document.createElement("video");
                video.className = "post-image expanded";
                video.controls = true;
                video.autoplay = true;
                video.src = link.dataset.video;

                var min = document.createElement("a");
                min.href = "#";
                min.textContent = "最小化";
                var control = document.createElement("div");
                control.className = "video-minimize";
                control.append("[", min, "]");

                min.addEventListener("click", function (ev) {
                    ev.preventDefault();
                    video.replaceWith(link); // restore the original thumbnail link
                    control.remove();
                });

                link.replaceWith(video);
                video.insertAdjacentElement("beforebegin", control);
            });
        });
    }

    function init() {
        enhanceImages();
        enhanceFlash();
        enhanceVideos();
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
