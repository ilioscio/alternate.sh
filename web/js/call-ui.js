// The call panel: phosphor-tinted 1-bit video beside the terminal — another
// window on the same imagined machine, not a foreign embed (DESIGN.md §9.2).

import { phosphorRGB } from "./themes.js";

const panel = () => document.getElementById("call-panel");

// A themed renderer for one 1-bit video surface. Frames are drawn at native
// resolution into a backing canvas; CSS upscales with image-rendering:
// pixelated for crisp fat pixels.
export class PhosphorScreen {
  constructor(canvas, theme) {
    this.canvas = canvas;
    this.ctx = canvas.getContext("2d");
    this.theme = theme;
    this.last = null; // {w, h, pix} — kept so a theme change can re-tint
  }

  setTheme(theme) {
    this.theme = theme;
    if (this.last) this.draw(this.last);
  }

  draw(frame) {
    this.last = frame;
    const { w, h, pix } = frame;
    if (this.canvas.width !== w || this.canvas.height !== h) {
      this.canvas.width = w;
      this.canvas.height = h;
    }
    const pal = phosphorRGB[this.theme] || phosphorRGB["white-black"];
    const img = this.ctx.createImageData(w, h);
    const d = img.data;
    const stride = w >> 3;
    for (let y = 0; y < h; y++) {
      for (let x = 0; x < w; x++) {
        const lit = pix[y * stride + (x >> 3)] & (0x80 >> (x & 7));
        const c = lit ? pal.fg : pal.bg;
        const o = (y * w + x) * 4;
        d[o] = c[0];
        d[o + 1] = c[1];
        d[o + 2] = c[2];
        d[o + 3] = 255;
      }
    }
    this.ctx.putImageData(img, 0, 0);
  }

  clear() {
    this.last = null;
    const pal = phosphorRGB[this.theme] || phosphorRGB["white-black"];
    this.ctx.fillStyle = `rgb(${pal.bg[0]},${pal.bg[1]},${pal.bg[2]})`;
    this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
  }
}

// showPanel opens the call panel for a call and returns its live pieces.
export function showPanel({ peer, media, theme, onHangup, onMute, onStyle }) {
  const p = panel();
  p.hidden = false;
  document.getElementById("terminal-screen").classList.add("call-active");

  document.getElementById("call-peer").textContent = peer;
  document.getElementById("call-status").textContent = "connected";
  const remote = new PhosphorScreen(document.getElementById("call-remote"), theme);
  const self = new PhosphorScreen(document.getElementById("call-self"), theme);
  remote.clear();
  self.clear();

  const videoEls = p.querySelectorAll(".call-video-only");
  for (const el of videoEls) el.hidden = media !== "av";

  // Duration ticker.
  const started = Date.now();
  const durEl = document.getElementById("call-duration");
  const tick = setInterval(() => {
    const s = Math.floor((Date.now() - started) / 1000);
    const mm = String(Math.floor(s / 60)).padStart(2, "0");
    const ss = String(s % 60).padStart(2, "0");
    durEl.textContent = `${mm}:${ss}`;
  }, 1000);
  durEl.textContent = "00:00";

  // Controls. Cloning removes any listeners from a previous call.
  const bind = (id, fn) => {
    const old = document.getElementById(id);
    const btn = old.cloneNode(true);
    old.replaceWith(btn);
    btn.addEventListener("click", () => fn(btn));
    return btn;
  };
  const muteBtn = bind("call-mute", (btn) => {
    const on = btn.dataset.on !== "1";
    btn.dataset.on = on ? "1" : "0";
    btn.textContent = on ? "[ unmute ]" : "[ mute ]";
    onMute(on);
  });
  muteBtn.dataset.on = "0";
  muteBtn.textContent = "[ mute ]";
  const styleBtn = bind("call-style", (btn) => {
    const shimmer = btn.dataset.shimmer !== "1";
    btn.dataset.shimmer = shimmer ? "1" : "0";
    btn.textContent = shimmer ? "[ style: shimmer ]" : "[ style: grain ]";
    onStyle(shimmer);
  });
  styleBtn.dataset.shimmer = "0";
  styleBtn.textContent = "[ style: grain ]";
  bind("call-hangup", () => onHangup());

  return {
    remote,
    self,
    setTheme(t) {
      remote.setTheme(t);
      self.setTheme(t);
    },
    setStatus(text) {
      document.getElementById("call-status").textContent = text;
    },
    close() {
      clearInterval(tick);
      p.hidden = true;
      document.getElementById("terminal-screen").classList.remove("call-active");
    },
  };
}
