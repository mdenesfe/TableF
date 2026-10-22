"""Draws the macOS installer (DMG) window background in TableF's style: the
deep-ocean field of 1s and 0s glowing from below, the brand mark, and an
arrow from the app to Applications.

Usage: python3 scripts/dmg/background.py <out_dir>
Writes background.png (660x400) and background@2x.png (1320x800).
Icon positions here must match scripts/dmg/make-dmg.sh.
"""
import math
import os
import random
import sys

from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 660, 400  # window content size in points
APP = (180, 215)  # icon centers in points (make-dmg.sh)
APPS = (480, 215)
BRAND = (10, 61, 98)
ICON = os.path.join(os.path.dirname(__file__), '..', '..', 'build', 'appicon.png')


def font(path, size):
    try:
        return ImageFont.truetype(path, size)
    except OSError:
        return ImageFont.load_default()


def render(scale):
    w, h = W * scale, H * scale
    s = lambda v: round(v * scale)  # noqa: E731

    # Deep-ocean gradient, lighter towards the bottom.
    top, bottom = (5, 26, 44), (14, 70, 110)
    img = Image.new('RGB', (w, h))
    d = ImageDraw.Draw(img)
    for y in range(h):
        t = (y / h) ** 1.3
        d.line([(0, y), (w, y)], fill=tuple(round(top[i] + (bottom[i] - top[i]) * t) for i in range(3)))

    # The 1/0 field, brighter in a glow under the icons.
    field = Image.new('L', (w, h), 0)
    fd = ImageDraw.Draw(field)
    mono = font('/System/Library/Fonts/SFNSMono.ttf', s(11))
    rnd = random.Random(3)
    cw, ch = s(9.5), s(14)
    for row in range(int(h / ch) + 1):
        for col in range(int(w / cw) + 1):
            if rnd.random() < 0.18:
                continue
            x, y = col * cw, row * ch
            dist = math.hypot((x - w / 2) / w, (y - h * 1.05) / h)
            glow = max(0.0, 1 - dist / 0.85) ** 1.7
            a = (0.04 + 0.42 * glow) * (0.4 + 0.6 * rnd.random())
            fd.text((x, y), rnd.choice('01'), font=mono, fill=round(255 * a))
    img = Image.composite(Image.new('RGB', (w, h), (150, 205, 245)), img, field)

    # Soft light pooling under the icons.
    pool = Image.new('L', (w, h), 0)
    ImageDraw.Draw(pool).ellipse([s(40), s(150), s(620), s(470)], fill=70)
    pool = pool.filter(ImageFilter.GaussianBlur(s(60)))
    img = Image.composite(Image.new('RGB', (w, h), (95, 160, 215)), img, pool)
    d = ImageDraw.Draw(img)

    # Quiet band behind the hint text so it reads over the digits.
    band = Image.new('L', (w, h), 0)
    ImageDraw.Draw(band).rounded_rectangle([s(150), s(300), s(510), s(336)], radius=s(18), fill=170)
    band = band.filter(ImageFilter.GaussianBlur(s(10)))
    img = Image.composite(Image.new('RGB', (w, h), (14, 62, 98)), img, band)
    d = ImageDraw.Draw(img)

    # Brand mark: the app icon itself (its rounded tile, without the margin).
    mx, my, ms = s(32), s(28), s(36)
    icon = Image.open(ICON).convert('RGBA')
    margin = round(icon.width * 100 / 1024)
    icon = icon.crop((margin, margin, icon.width - margin, icon.height - margin)).resize((ms, ms), Image.LANCZOS)
    img.paste(icon, (mx, my), icon)
    d = ImageDraw.Draw(img)
    d.text((mx + ms + s(12), my + s(2)), 'TableF', font=font('/System/Library/Fonts/SFNS.ttf', s(20)), fill='white')
    d.text((mx + ms + s(12), my + s(26)), 'Open-source database client', font=font('/System/Library/Fonts/SFNS.ttf', s(11)), fill=(170, 200, 225))

    # Arrow from the app to Applications.
    y = s(APP[1])
    x0, x1 = s(APP[0] + 78), s(APPS[0] - 78)
    dash, gap = s(7), s(6)
    x = x0
    while x < x1 - s(12):
        d.line([(x, y), (min(x + dash, x1 - s(12)), y)], fill=(225, 238, 250), width=s(2))
        x += dash + gap
    d.polygon([(x1, y), (x1 - s(12), y - s(7)), (x1 - s(12), y + s(7))], fill=(225, 238, 250))

    # Hint under the arrow, between the icon labels.
    d.text((s(W / 2), s(318)), 'Drag TableF to Applications to install', font=font('/System/Library/Fonts/SFNS.ttf', s(12)),
           fill=(200, 222, 240), anchor='mm')
    return img


if __name__ == '__main__':
    out = sys.argv[1]
    os.makedirs(out, exist_ok=True)
    render(1).save(os.path.join(out, 'background.png'))
    render(2).save(os.path.join(out, 'background@2x.png'))
    print('ok')
