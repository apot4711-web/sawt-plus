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

    synchronized void start() {
        if (running) {
            return;
        }

        int channelMask = channels == 2 ? AudioFormat.CHANNEL_IN_STEREO : AudioFormat.CHANNEL_IN_MONO;
        int minBuffer = AudioRecord.getMinBufferSize(rate, channelMask, AudioFormat.ENCODING_PCM_16BIT);
        if (minBuffer <= 0) {
            listener.onError("AudioRecord does not support " + rate + " Hz");
            return;
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
            return;
        }

        running = true;
        recorder.startRecording();

        final int readSize = frameBytes;
        thread = new Thread(() -> {
            Process.setThreadPriority(Process.THREAD_PRIORITY_URGENT_AUDIO);
            byte[] buffer = new byte[readSize];
            while (running) {
                int n = recorder.read(buffer, 0, buffer.length);
                if (n > 0) {
                    listener.onFrame(buffer, n);
                } else if (n < 0) {
                    if (running) {
                        listener.onError("AudioRecord read error: " + n);
                    }
                    break;
                }
            }
        }, "sawt-capture");
        thread.start();
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
