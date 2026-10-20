package com.sawtplus.app;

import android.content.Context;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;

import java.util.concurrent.CopyOnWriteArrayList;

/**
 * Fans out captured PCM frames to every connected transport and starts/stops
 * the microphone when clients come and go.
 */
final class StreamHub {
    interface Sink {
        /**
         * @return false when the frame was dropped or the sink is closed.
         */
        boolean send(byte[] data, int length);

        void close();
    }

    private static final String TAG = App.TAG;
    private static final long STOP_GRACE_MS = 1500;

    private final CopyOnWriteArrayList<Sink> sinks = new CopyOnWriteArrayList<>();
    private final AudioCapture capture;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Runnable stopCapture = this::maybeStopCapture;
    private boolean stopScheduled;
    private volatile boolean closed;
    private byte[] silence;

    StreamHub(Context context) {
        capture = new AudioCapture(App.RATE, App.CHANNELS, App.FRAME_MS,
                new AudioCapture.Listener() {
                    @Override
                    public void onFrame(byte[] data, int length) {
                        dispatch(data, length);
                    }

                    @Override
                    public void onError(String message) {
                        Log.e(TAG, message);
                        ServiceState.error = message;
                    }
                });
    }

    void addSink(Sink sink) {
        if (closed || sinks.contains(sink)) {
            return;
        }
        sinks.add(sink);
        synchronized (this) {
            stopScheduled = false;
            handler.removeCallbacks(stopCapture);
        }
        ServiceState.clients = sinks.size();
        startCapture();
    }

    void removeSink(Sink sink) {
        if (sink == null) {
            return;
        }
        sinks.remove(sink);
        sink.close();
        ServiceState.clients = sinks.size();
        if (sinks.isEmpty()) {
            synchronized (this) {
                if (!stopScheduled) {
                    stopScheduled = true;
                    handler.postDelayed(stopCapture, STOP_GRACE_MS);
                }
            }
        }
    }

    private void startCapture() {
        if (closed) {
            return;
        }
        if (!capture.isRunning()) {
            capture.start();
            Log.i(TAG, "microphone capture started");
        }
    }

    private void maybeStopCapture() {
        synchronized (this) {
            stopScheduled = false;
            if (!sinks.isEmpty()) {
                return;
            }
        }
        capture.stop();
        Log.i(TAG, "microphone capture stopped");
    }

    private void dispatch(byte[] data, int length) {
        ServiceState.frames++;
        byte[] frame = data;
        if (ServiceState.muted) {
            if (silence == null || silence.length < length) {
                silence = new byte[length];
            }
            frame = silence;
        } else if (ServiceState.gain != 1f) {
            applyGain(data, length, ServiceState.gain);
        }
        for (Sink sink : sinks) {
            if (!sink.send(frame, length)) {
                ServiceState.dropped++;
            }
        }
    }

    private static void applyGain(byte[] data, int length, float gain) {
        for (int i = 0; i + 1 < length; i += 2) {
            int sample = (short) ((data[i] & 0xff) | (data[i + 1] << 8));
            int scaled = Math.round(sample * gain);
            if (scaled > 32767) {
                scaled = 32767;
            } else if (scaled < -32768) {
                scaled = -32768;
            }
            data[i] = (byte) (scaled & 0xff);
            data[i + 1] = (byte) ((scaled >> 8) & 0xff);
        }
    }

    void shutdown() {
        closed = true;
        handler.removeCallbacks(stopCapture);
        for (Sink sink : sinks) {
            sink.close();
        }
        sinks.clear();
        capture.stop();
        ServiceState.clients = 0;
    }

    boolean isCapturing() {
        return capture.isRunning();
    }
}
