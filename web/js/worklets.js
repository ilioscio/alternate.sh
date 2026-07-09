// AudioWorklet processors for the call pipeline (DESIGN.md §9.3). Loaded via
// audioWorklet.addModule('/js/worklets.js') — this file runs on the audio
// rendering thread and cannot import the other modules.
//
// The codec rate is 8kHz mono; the AudioContext runs at whatever rate the
// hardware prefers (usually 48kHz), so both processors resample linearly by
// the ratio the main thread passes in. The capture side also applies the
// lofi coloring that follows the bandpass filters: soft saturation and a
// bit-crush, so what gets encoded already sounds like 1979.

const CODEC_RATE = 8000;
const CHUNK_SAMPLES = 160; // 20ms at 8kHz

// capture-processor: context-rate float in → 8kHz Int16 chunks out (port).
class CaptureProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super();
    this.ratio = (options.processorOptions?.contextRate || sampleRate) / CODEC_RATE;
    this.pos = 0;        // fractional read position into the incoming stream
    this.tail = null;    // last input block, for interpolation across blocks
    this.chunk = new Int16Array(CHUNK_SAMPLES);
    this.fill = 0;
    this.muted = false;
    this.port.onmessage = (e) => {
      if (e.data.type === "mute") this.muted = e.data.on;
    };
  }

  color(s) {
    // Soft saturation: tanh-ish drive for intercom grit.
    s = Math.tanh(s * 1.8);
    // Bit-crush to ~8 bits: the quantization hiss is part of the sound.
    return Math.round(s * 127) / 127;
  }

  process(inputs) {
    const input = inputs[0];
    if (!input || !input[0]) return true;
    const cur = input[0];

    // Concatenate the previous block's tail so interpolation can straddle
    // block boundaries.
    const prev = this.tail;
    const prevLen = prev ? prev.length : 0;
    const total = prevLen + cur.length;

    const at = (i) => (i < prevLen ? prev[i] : cur[i - prevLen]);

    while (this.pos + 1 < total) {
      const i = Math.floor(this.pos);
      const frac = this.pos - i;
      let s = at(i) * (1 - frac) + at(i + 1) * frac;
      s = this.muted ? 0 : this.color(s);
      const v = Math.max(-1, Math.min(1, s));
      this.chunk[this.fill++] = (v * 32767) | 0;
      if (this.fill === CHUNK_SAMPLES) {
        this.port.postMessage(this.chunk.slice());
        this.fill = 0;
      }
      this.pos += this.ratio;
    }

    // Keep only what interpolation still needs.
    this.pos -= prevLen;
    this.tail = cur.slice();
    return true;
  }
}

// playback-processor: 8kHz Int16 chunks in (port) → context-rate float out.
// A small jitter buffer smooths network arrival; underruns play silence and
// overruns drop the oldest audio (stale realtime audio is worthless).
class PlaybackProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super();
    this.ratio = (options.processorOptions?.contextRate || sampleRate) / CODEC_RATE;
    this.queue = [];       // Int16Array chunks awaiting playback
    this.queued = 0;       // samples across queue
    this.pos = 0;          // fractional read position into queue[0]
    this.started = false;  // wait for a small prebuffer before starting
    this.port.onmessage = (e) => {
      this.queue.push(e.data);
      this.queued += e.data.length;
      // Bound latency: beyond ~200ms, drop from the front.
      while (this.queued > CODEC_RATE / 5) {
        const dropped = this.queue.shift();
        this.queued -= dropped.length;
        this.pos = 0;
      }
    };
  }

  sampleAt(idx) {
    // idx indexes the concatenated queue; returns 0 past the end.
    for (const c of this.queue) {
      if (idx < c.length) return c[idx] / 32768;
      idx -= c.length;
    }
    return 0;
  }

  process(_inputs, outputs) {
    const out = outputs[0][0];
    if (!out) return true;

    if (!this.started) {
      // Prebuffer ~60ms so a network hiccup doesn't immediately underrun.
      if (this.queued < (CODEC_RATE * 3) / 50) {
        out.fill(0);
        return true;
      }
      this.started = true;
    }

    for (let i = 0; i < out.length; i++) {
      const idx = Math.floor(this.pos);
      const frac = this.pos - idx;
      out[i] = this.sampleAt(idx) * (1 - frac) + this.sampleAt(idx + 1) * frac;
      this.pos += 1 / this.ratio;
    }

    // Release fully-played chunks.
    while (this.queue.length && this.pos >= this.queue[0].length) {
      this.pos -= this.queue[0].length;
      this.queued -= this.queue[0].length;
      this.queue.shift();
    }
    if (this.queue.length === 0) {
      this.pos = 0;
      this.started = false; // rebuffer after an underrun
    }
    return true;
  }
}

registerProcessor("capture-processor", CaptureProcessor);
registerProcessor("playback-processor", PlaybackProcessor);
