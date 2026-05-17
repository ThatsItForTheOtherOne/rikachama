#!/bin/sh
set -e

case "$1" in
    image)
        convert - -strip "jpg:-"
        ;;
    image-gif)
        convert - -strip "gif:-"
        ;;
    image-thumb)
        convert - -strip -resize 250x250 "jpg:-"
        ;;
    video)
        # mp4 demuxer needs to seek for moov-at-end files (common on phones/Twitter),
        # so buffer stdin to tmpfs and pass a seekable path to ffmpeg.
        tmp=$(mktemp /tmp/in.XXXXXX)
        trap 'rm -f "$tmp"' EXIT
        cat > "$tmp"
        ffmpeg -i "$tmp" -c:v libvpx-vp9 -c:a libopus -f webm pipe:1
        ;;
    video-thumb)
        tmp=$(mktemp /tmp/in.XXXXXX)
        trap 'rm -f "$tmp"' EXIT
        cat > "$tmp"
        ffmpeg -i "$tmp" -vf "thumbnail" -frames:v 1 -f image2 pipe:1 \
            | convert - -strip -resize 250x250 "jpg:-"
        ;;
    pdf)
        gs -q -dNOPAUSE -dBATCH -dSAFER -sDEVICE=pdfwrite \
           -sOutputFile=- -
        ;;
    pdf-thumb)
        gs -q -dNOPAUSE -dBATCH -dSAFER -sDEVICE=jpeg -dFirstPage=1 -dLastPage=1 \
           -sOutputFile=- - | convert - -resize 250x250 "jpg:-"
        ;;
    *)
        echo "unknown tool: $1" >&2
        exit 1
        ;;
esac
