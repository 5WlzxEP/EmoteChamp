# Emote Champ

Displays images on screen, wayland only.


run via docker:
```shell
docker run -v "$XDG_RUNTIME_DIR/wayland-0:$XDG_RUNTIME_DIR/wayland-0" -e XDG_RUNTIME_DIR="$XDG_RUNTIME_DIR" -p 8080:8080 ghcr.io/5wlzxep/emote-champ:latest
 ```

## Things it can do

- Scale images/animations 
- play audio via webbrowser
- Set icon or show no icon at all
- Toggle between decorations


### Supported Formats

#### Image

- GIF
- JPEG
- PNG
- Webp

#### Audio

- MP3
- OGG
- WAV
- Flac

### Known issues:

- GIFs may render incorrectly
- Docker upload of files larger than 10 MB may fail because /tmp isn't working as expected
- Repeated switching between SetDecoration shrinks the window until invisible