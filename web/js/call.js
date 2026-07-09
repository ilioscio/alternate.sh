// Call orchestrator: reacts to call-start / call-end control messages from
// the terminal WebSocket, runs the capture→stylize→encode pipeline, and
// relays media packets over the dedicated /ws/call socket (DESIGN.md §9.4).
//
// The terminal remains the control surface — this module never initiates or
// accepts a call; it only gives an active call its eyes, ears, and voice.

import { onControl, onThemeChange, getTheme, getToken } from "./app.js";
import { showPanel } from "./call-ui.js";
import { ditherBlueNoise, ditherFloydSteinberg } from "./dither.js";
import { VideoEncoder, VideoDecoder } from "./videocodec.js";
import { AudioEncoder, decodeAudio, CHUNK_SAMPLES } from "./audiocodec.js";
import { parsePacket, KIND_VIDEO_KEY, KIND_VIDEO_DELTA, KIND_AUDIO } from "./packets.js";

// Skip a video frame when this much is already queued on the socket —
// sending stale frames only adds latency (the wire-level FlagDroppable
// equivalent for the browser leg).
const VIDEO_BACKPRESSURE_BYTES = 32 * 1024;

let active = null; // the single live call's teardown state

function connectMediaWS(callId) {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(
    `${proto}//${location.host}/ws/call?token=${encodeURIComponent(getToken())}&call=${encodeURIComponent(callId)}`,
  );
  ws.binaryType = "arraybuffer";
  return ws;
}

async function startCall(msg) {
  if (active) teardown("replaced");

  const { width, height, fps } = msg.params;
  const wantVideo = msg.media === "av";

  const state = {
    id: msg.callId,
    ws: null,
    stream: null,
    audioCtx: null,
    captureNode: null,
    videoTimer: null,
    panel: null,
    shimmer: false,
    closed: false,
  };
  active = state;

  const panel = showPanel({
    peer: msg.peer,
    media: msg.media,
    theme: getTheme(),
    onHangup: () => teardown("hang up"),
    onMute: (on) => state.captureNode?.port.postMessage({ type: "mute", on }),
    onStyle: (shimmer) => {
      state.shimmer = shimmer;
    },
  });
  state.panel = panel;

  // Media socket first: if it fails there is nothing to capture for.
  const ws = connectMediaWS(msg.callId);
  state.ws = ws;
  ws.onclose = () => teardown("call ended");
  ws.onerror = () => teardown("media connection failed");

  const decoder = new VideoDecoder();
  ws.onmessage = (e) => {
    let p;
    try {
      p = parsePacket(new Uint8Array(e.data));
    } catch {
      return;
    }
    if (p.kind === KIND_AUDIO) {
      try {
        const pcm = decodeAudio(p);
        state.playbackNode?.port.postMessage(pcm, [pcm.buffer]);
      } catch {}
    } else if (p.kind === KIND_VIDEO_KEY || p.kind === KIND_VIDEO_DELTA) {
      try {
        const frame = decoder.decode(p);
        if (frame) panel.remote.draw(frame);
      } catch {}
    }
  };

  // Capture. Constraints are a hint; we crop/scale to the negotiated size.
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true },
      video: wantVideo ? { width: { ideal: 640 }, height: { ideal: 480 }, frameRate: { ideal: fps } } : false,
    });
  } catch (err) {
    panel.setStatus("mic/camera denied — receiving only");
    stream = null;
  }
  state.stream = stream;

  // ── Audio pipeline ──────────────────────────────────────────────────────
  // One context for both directions, at the hardware's preferred rate; the
  // worklets resample to/from the 8kHz codec rate.
  const audioCtx = new AudioContext();
  state.audioCtx = audioCtx;
  // Autoplay policy can start the context suspended if the browser doesn't
  // count terminal typing as engagement; resume now and on the next input.
  if (audioCtx.state === "suspended") {
    audioCtx.resume().catch(() => {});
    const resume = () => {
      audioCtx.resume().catch(() => {});
      document.removeEventListener("keydown", resume);
      document.removeEventListener("pointerdown", resume);
    };
    document.addEventListener("keydown", resume);
    document.addEventListener("pointerdown", resume);
  }
  await audioCtx.audioWorklet.addModule("/js/worklets.js");

  const playback = new AudioWorkletNode(audioCtx, "playback-processor", {
    numberOfInputs: 0,
    outputChannelCount: [1],
    processorOptions: { contextRate: audioCtx.sampleRate },
  });
  playback.connect(audioCtx.destination);
  state.playbackNode = playback;

  if (stream && stream.getAudioTracks().length > 0) {
    const src = audioCtx.createMediaStreamSource(stream);
    // The telephone band: 300–3400 Hz, two biquads.
    const hp = new BiquadFilterNode(audioCtx, { type: "highpass", frequency: 300, Q: 0.7 });
    const lp = new BiquadFilterNode(audioCtx, { type: "lowpass", frequency: 3400, Q: 0.7 });
    const capture = new AudioWorkletNode(audioCtx, "capture-processor", {
      numberOfOutputs: 0,
      processorOptions: { contextRate: audioCtx.sampleRate },
    });
    src.connect(hp).connect(lp).connect(capture);
    state.captureNode = capture;

    const aenc = new AudioEncoder({ source: msg.source });
    capture.port.onmessage = (e) => {
      if (state.closed || ws.readyState !== WebSocket.OPEN) return;
      if (e.data.length !== CHUNK_SAMPLES) return;
      ws.send(aenc.encodeChunk(e.data));
    };
  }

  // ── Video pipeline ──────────────────────────────────────────────────────
  if (stream && wantVideo && stream.getVideoTracks().length > 0) {
    const videoEl = document.createElement("video");
    videoEl.muted = true;
    videoEl.playsInline = true;
    videoEl.srcObject = stream;
    await videoEl.play().catch(() => {});

    const work = document.createElement("canvas");
    work.width = width;
    work.height = height;
    const wctx = work.getContext("2d", { willReadFrequently: true });
    const venc = new VideoEncoder({ source: msg.source });
    const gray = new Uint8Array(width * height);

    state.videoTimer = setInterval(() => {
      if (state.closed || ws.readyState !== WebSocket.OPEN) return;
      if (videoEl.readyState < 2) return;
      if (ws.bufferedAmount > VIDEO_BACKPRESSURE_BYTES) return; // shed the frame

      // Center-crop the camera image to the codec's aspect, then scale.
      const vw = videoEl.videoWidth, vh = videoEl.videoHeight;
      if (!vw || !vh) return;
      const targetAspect = width / height;
      let sw = vw, sh = vh, sx = 0, sy = 0;
      if (vw / vh > targetAspect) {
        sw = vh * targetAspect;
        sx = (vw - sw) / 2;
      } else {
        sh = vw / targetAspect;
        sy = (vh - sh) / 2;
      }
      wctx.drawImage(videoEl, sx, sy, sw, sh, 0, 0, width, height);
      const rgba = wctx.getImageData(0, 0, width, height).data;
      // Rec.601 integer luma.
      for (let i = 0, j = 0; i < gray.length; i++, j += 4) {
        gray[i] = (rgba[j] * 77 + rgba[j + 1] * 150 + rgba[j + 2] * 29) >> 8;
      }
      const dither = state.shimmer ? ditherFloydSteinberg : ditherBlueNoise;
      const pix = dither(gray, width, height);
      panel.self.draw({ w: width, h: height, pix });
      const pkt = venc.encode(pix, width, height);
      if (pkt) ws.send(pkt);
    }, 1000 / fps);
  }
}

function teardown(reason) {
  const state = active;
  if (!state || state.closed) return;
  state.closed = true;
  active = null;

  if (state.videoTimer) clearInterval(state.videoTimer);
  if (state.ws && state.ws.readyState <= WebSocket.OPEN) state.ws.close();
  if (state.stream) for (const t of state.stream.getTracks()) t.stop();
  if (state.audioCtx && state.audioCtx.state !== "closed") state.audioCtx.close().catch(() => {});
  if (state.panel) state.panel.close();
}

onControl("call-start", (msg) => {
  startCall(msg).catch(() => teardown("setup failed"));
});
onControl("call-end", (msg) => {
  if (active && active.id === msg.callId) teardown(msg.reason || "call ended");
});
onThemeChange((t) => active?.panel?.setTheme(t));
