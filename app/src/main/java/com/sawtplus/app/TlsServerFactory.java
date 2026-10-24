package com.sawtplus.app;

import org.java_websocket.SSLSocketChannel2;
import org.java_websocket.server.DefaultSSLWebSocketServerFactory;

import java.io.IOException;
import java.nio.channels.ByteChannel;
import java.nio.channels.SelectionKey;
import java.nio.channels.SocketChannel;
import java.util.ArrayList;
import java.util.List;

import javax.net.ssl.SSLContext;
import javax.net.ssl.SSLEngine;

/**
 * SSL WebSocket server factory that restricts handshakes to TLS 1.2 and
 * higher. (Depends on Java-WebSocket 1.5.x internals: the protected
 * {@code sslcontext}/{@code exec} fields of DefaultSSLWebSocketServerFactory
 * and the 4-arg SSLSocketChannel2 constructor.)
 */
final class TlsServerFactory extends DefaultSSLWebSocketServerFactory {

    TlsServerFactory(SSLContext sslContext) {
        super(sslContext);
    }

    @Override
    public ByteChannel wrapChannel(SocketChannel channel, SelectionKey key) throws IOException {
        SSLEngine engine = sslcontext.createSSLEngine();
        engine.setUseClientMode(false);
        // TLS 1.2+ only. TLS 1.3 exists only on API 29+, and enabling a
        // protocol the platform does not know would throw
        // IllegalArgumentException, so intersect with the supported set.
        List<String> protocols = new ArrayList<>();
        for (String protocol : engine.getSupportedProtocols()) {
            if ("TLSv1.2".equals(protocol) || "TLSv1.3".equals(protocol)) {
                protocols.add(protocol);
            }
        }
        if (!protocols.isEmpty()) {
            engine.setEnabledProtocols(protocols.toArray(new String[0]));
        }
        return new SSLSocketChannel2(channel, engine, exec, key);
    }
}
