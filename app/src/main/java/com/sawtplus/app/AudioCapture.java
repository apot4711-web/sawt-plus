package com.sawtplus.app;

import android.media.AudioFormat;
import android.media.AudioRecord;
import android.media.MediaRecorder;
import android.os.Process;
import android.util.Log;

/**
 * Captures 16-bit PCM frames and delivers them to a listener.
 */
final class AudioCapture {
    interface Listener {
        void onFrame(byte[] data, int length);

        void onError(String message);

        /**
         * Called when the capture thread dies from a read error (e.g. the
         * microphone was preempted by a phone call), or when
         * startRecording() throws during start() (e.g. the mic is briefly
         * held by another app). The capture has already been marked stopped
         * and the recorder released at this point; the error is considered
         * transient and the listener may try to restart capture.
         */
        void onCaptureInterrupted();
    }

    private static final String TAG = App.TAG;

    private final int rate;
    private final int channels;
    private final int frameMs;
    private final Listener listener;

    private AudioRecord recorder;
    private Thread thread;
    private volatile boolean running;

    AudioCapture(int rate, int channels, int frameMs, Listener listener) {
        this.rate = rate;
        this.channels = channels;
        this.frameMs = frameMs;
        this.listener = listener;
    }

    synchronized boolean isRunning() {
        return running;
    }

    void start() {
        boolean startFailed;
        synchronized (this) {
            startFailed = startLocked();
        }
        // Notify outside the lock: the listener (StreamHub) takes its own
        // monitor in onCaptureInterrupted(), so notifying under this lock
        // would risk a lock-ordering hazard. Matches handleReadError().
        if (startFailed) {
            listener.onCaptureInterrupted();
        }
    }

    /**
     * Brings the recorder up. Must be called with the AudioCapture monitor
     * held. Returns true when startRecording() threw; the recorder has been
     * released and running reset to false in that case, and the caller must
     * notify the listener outside the lock so the interruption/restart path
     * can retry.
     */
    private boolean startLocked() {
        if (running) {
            return false;
        }

        int channelMask = channels == 2 ? AudioFormat.CHANNEL_IN_STEREO : AudioFormat.CHANNEL_IN_MONO;
        int minBuffer = AudioRecord.getMinBufferSize(rate, channelMask, AudioFormat.ENCODING_PCM_16BIT);
        if (minBuffer <= 0) {
            listener.onError("AudioRecord does not support " + rate + " Hz");
            return false;
        }

        int frameBytes = rate * frameMs / 1000 * channels * 2;
        int bufferSize = Math.max(minBuffer * 2, frameBytes * 4);

        int[] sources = new int[]{
                MediaRecorder.AudioSource.DEFAULT,
                MediaRecorder.AudioSource.MIC,
                MediaRecorder.AudioSource.VOICE_RECOGNITION,
        };

        for (int source : sources) {
            AudioRecord candidate = null;
            try {
                candidate = new AudioRecord(source, rate, channelMask,
                        AudioFormat.ENCODING_PCM_16BIT, bufferSize);
                if (candidate.getState() == AudioRecord.STATE_INITIALIZED) {
                    recorder = candidate;
                    break;
                }
            } catch (Exception e) {
                Log.w(TAG, "AudioRecord source " + source + " failed", e);
            }
            if (candidate != null) {
                candidate.release();
            }
        }

        if (recorder == null) {
            listener.onError("Could not initialize the microphone (is RECORD_AUDIO granted?)");
            return false;
        }

        running = true;
        try {
            recorder.startRecording();
        } catch (IllegalStateException | SecurityException e) {
            // startRecording() can throw when the mic is momentarily
            // unavailable (permission revoked at runtime, another app holding
            // it exclusively). Tear down and report a start failure instead
            // of letting the exception escape: on the ADB path that would
            // kill the server thread, and on the Wi-Fi path it would abort
            // the restart callback. Routed through onCaptureInterrupted() so
            // the existing retry scheduler (capped attempts + backoff)
            // handles it like any other interruption.
            Log.w(TAG, "AudioRecord.startRecording() failed", e);
            try {
                recorder.release();
            } catch (Exception ignored) {
            }
            recorder = null;
            running = false;
            return true;
        }

        final int readSize = frameBytes;
        thread = new Thread(() -> {
            Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO);
            byte[] buffer = new byte[readSize];
            while (running) {
                int n = recorder.read(buffer, 0, buffer.length);
                if (n > 0) {
                    listener.onFrame(buffer, n);
                } else if (n < 0) {
                    handleReadError("AudioRecord read error: " + n);
                    break;
                }
            }
        }, "sawt-capture");
        thread.start();
        return false;
    }

    /**
     * Called from the capture thread when AudioRecord.read() fails (e.g. the
     * microphone was preempted by a phone call). Marks the capture as stopped
     * and releases the recorder — a fresh AudioRecord is required to recover —
     * then notifies the listener so it can attempt a restart. The listener is
     * notified outside the lock to avoid lock-ordering hazards. Idempotent: a
     * no-op if stop() already ran (which also unblocks the read with an
     * error).
     */
    private void handleReadError(String message) {
        boolean notify;
        synchronized (this) {
            notify = running;
            if (running) {
                running = false;
                try {
                    if (recorder != null) {
                        recorder.release();
                    }
                } catch (Exception ignored) {
                }
                recorder = null;
            }
        }
        if (notify) {
            Log.w(TAG, message);
            listener.onCaptureInterrupted();
        }
    }

    synchronized void stop() {
        if (!running) {
            return;
        }
        running = false;
        try {
            if (recorder != null) {
                recorder.stop();
            }
        } catch (Exception ignored) {
        }
        if (thread != null) {
            try {
                thread.join(1000);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            thread = null;
        }
        if (recorder != null) {
            recorder.release();
            recorder = null;
        }
    }
}
