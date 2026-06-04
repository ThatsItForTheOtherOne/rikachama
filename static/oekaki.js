(function () {
    'use strict';

    function attachFile(inputName, bytes, filename, mimeType) {
        var input = document.querySelector('input[name="' + inputName + '"]');
        if (!input) return;
        var file = new File([bytes], filename, { type: mimeType });
        var dt = new DataTransfer();
        dt.items.add(file);
        input.files = dt.files;
    }

    function openOekaki() {
        if (typeof Tegaki === 'undefined') {
            console.error('Tegaki not loaded');
            return;
        }
        Tegaki.open({
            width: 400,
            height: 400,
            saveReplay: true,
            onDone: function () {
                Tegaki.flatten().toBlob(function (imageBlob) {
                    attachFile('upfile', imageBlob, 'oekaki.png', 'image/png');
                }, 'image/png');

                if (Tegaki.replayRecorder) {
                    var replayBlob = Tegaki.replayRecorder.toBlob();
                    if (replayBlob) {
                        attachFile('replay_file', replayBlob, 'oekaki.tgkr', 'application/octet-stream');
                    }
                }
            },
            onCancel: function () {}
        });
    }

    document.addEventListener('DOMContentLoaded', function () {
        var btn = document.getElementById('oekaki-btn');
        if (btn) btn.addEventListener('click', openOekaki);
    });
})();
