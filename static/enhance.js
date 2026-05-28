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

    // The media template renders: <small class="thumbnail-notice">...</small><br><a>...</a>.
    // When the link "activates" (expands / plays), the preceding notice + <br> should hide.
    function noticeBefore(link) {
        var br = link.previousElementSibling;
        if (!br || br.tagName !== "BR") return null;
        var notice = br.previousElementSibling;
        if (!notice || !notice.classList || !notice.classList.contains("thumbnail-notice")) return null;
        return { notice: notice, br: br };
    }
    function setNoticeVisible(n, visible) {
        if (!n) return;
        n.notice.style.display = visible ? "" : "none";
        n.br.style.display = visible ? "" : "none";
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
            var n = noticeBefore(link);

            link.addEventListener("click", function (e) {
                if (!isPlainClick(e)) return;
                e.preventDefault();
                expanded = !expanded;
                if (expanded) {
                    img.src = fullSrc;
                    img.removeAttribute("width");
                    img.removeAttribute("height");
                    img.classList.add("expanded");
                    setNoticeVisible(n, false);
                } else {
                    img.src = thumbSrc;
                    if (thumbW) img.setAttribute("width", thumbW);
                    if (thumbH) img.setAttribute("height", thumbH);
                    img.classList.remove("expanded");
                    setNoticeVisible(n, true);
                }
            });
        });
    }

    // a.swf-embed: replace the thumbnail with an inline Ruffle player on click.
    function enhanceFlash() {
        document.querySelectorAll("a.swf-embed").forEach(function (link) {
            var n = noticeBefore(link);
            link.addEventListener("click", function (e) {
                // Ruffle missing -> let the browser follow href and download the .swf.
                if (!window.RufflePlayer) return;
                if (!isPlainClick(e)) return;
                e.preventDefault();

                var player = window.RufflePlayer.newest().createPlayer();
                player.className = "post-image expanded";
                // Ruffle needs an explicit size or the player renders at 0x0.
                // Start with a sensible default for the load window, then snap to
                // the SWF's real stage size once Ruffle parses the header.
                player.style.width = "550px";
                player.style.height = "400px";
                player.addEventListener("loadedmetadata", function () {
                    var m = player.metadata;
                    if (m && m.width && m.height) {
                        player.style.width = m.width + "px";
                        player.style.height = m.height + "px";
                    }
                });

                var min = document.createElement("a");
                min.href = "#";
                min.textContent = (window.i18n && window.i18n["post.minimize"]) || "Minimize";
                var control = document.createElement("div");
                control.className = "video-minimize";
                control.append("[", min, "]");

                min.addEventListener("click", function (ev) {
                    ev.preventDefault();
                    player.replaceWith(link); // restore the original thumbnail link
                    control.remove();
                    setNoticeVisible(n, true);
                });

                link.replaceWith(player);
                player.insertAdjacentElement("beforebegin", control);
                setNoticeVisible(n, false);
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
            var n = noticeBefore(link);
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
                min.textContent = (window.i18n && window.i18n["post.minimize"]) || "Minimize";
                var control = document.createElement("div");
                control.className = "video-minimize";
                control.append("[", min, "]");

                min.addEventListener("click", function (ev) {
                    ev.preventDefault();
                    video.replaceWith(link); // restore the original thumbnail link
                    control.remove();
                    setNoticeVisible(n, true);
                });

                link.replaceWith(video);
                video.insertAdjacentElement("beforebegin", control);
                setNoticeVisible(n, false);
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
