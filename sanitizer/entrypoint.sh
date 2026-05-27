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
        tmp=$(mktemp /tmp/in.XXXXXX)
        out=$(mktemp /tmp/out.XXXXXX)
        trap 'rm -f "$tmp" "$out"' EXIT
        cat > "$tmp"
        ffmpeg -i "$tmp" -c:v libx264 -pix_fmt yuv420p -c:a aac -movflags +faststart -f mp4 -y "$out"
        cat "$out"
        ;;
    video-thumb)
        tmp=$(mktemp /tmp/in.XXXXXX)
        trap 'rm -f "$tmp"' EXIT
        cat > "$tmp"
        ffmpeg -i "$tmp" -vf "thumbnail" -frames:v 1 -update 1 -f image2 pipe:1 \
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
