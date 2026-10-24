package com.sawtplus.app;

import android.content.Context;
import android.os.Build;
import android.util.Log;

import java.math.BigInteger;
import java.security.KeyPairGenerator;
import java.security.KeyStore;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.security.cert.Certificate;
import java.security.cert.X509Certificate;
import java.util.Calendar;

import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.SSLContext;
import javax.security.auth.x500.X500Principal;

/**
 * Self-signed TLS identity for the Wi-Fi WebSocket server (wss://).
 *
 * <p>On first use, generates a keypair and self-signed certificate in the
 * app-private AndroidKeyStore and reuses it afterwards, so the certificate
 * fingerprint is stable across restarts:
 * <ul>
 *   <li>API 23+: EC P-256 (KeyGenParameterSpec; AndroidKeyStore creates the
 *       self-signed certificate automatically).</li>
 *   <li>API 21-22: RSA-2048 (KeyPairGeneratorSpec; AndroidKeyStore did not
 *       support EC keys before API 23).</li>
 * </ul>
 * The certificate is NOT anchored to any CA. Clients must pin the SHA-256
 * fingerprint (TOFU) — see WifiServer's class Javadoc for the wire contract.
 * A 25-year validity is used because expiry is handled by re-pinning, not by
 * CA validation.
 *
 * <p>API 23+ keys authorize {@code DIGEST_NONE}: the TLS stack (Conscrypt)
 * hashes the handshake itself and asks the keystore to sign the pre-hashed
 * value, so a key restricted to SHA-256 cannot be used for server
 * authentication. The alias was bumped when this changed; the previous key
 * (aliased {@code sawtplus_tls}) is deleted so the fingerprint changes
 * deterministically.
 */
final class TlsCertificate {
    private static final String TAG = App.TAG;
    private static final String ALIAS = "sawtplus_tls_v2";
    private static final String LEGACY_ALIAS = "sawtplus_tls";
    private static final String KEYSTORE_PROVIDER = "AndroidKeyStore";
    /** Regenerate the certificate when it expires within this window. */
    private static final long REFRESH_BEFORE_MS = 30L * 24 * 3600 * 1000;
    private static final int VALIDITY_YEARS = 25;

    private static String cachedFingerprint;

    private TlsCertificate() {
    }

    /**
     * SHA-256 fingerprint of the server certificate, lowercase hex
     * (64 chars). Generates the keypair on first call.
     */
    static synchronized String fingerprint(Context context, String deviceId) throws Exception {
        if (cachedFingerprint != null) {
            return cachedFingerprint;
        }
        deleteLegacyKey();
        X509Certificate cert = loadValid();
        if (cert == null) {
            generate(context.getApplicationContext(), deviceId);
            cert = loadValid();
        }
        if (cert == null) {
            throw new IllegalStateException("TLS certificate unavailable");
        }
        MessageDigest md = MessageDigest.getInstance("SHA-256");
        cachedFingerprint = toHex(md.digest(cert.getEncoded()));
        return cachedFingerprint;
    }

    /**
     * TLS server context whose KeyManager serves the self-signed certificate.
     * The caller is responsible for restricting protocols/ciphers (see
     * TlsFrontend, which enforces TLS 1.2+).
     */
    static synchronized SSLContext serverContext(Context context, String deviceId) throws Exception {
        fingerprint(context, deviceId); // ensure key + certificate exist
        KeyStore ks = KeyStore.getInstance(KEYSTORE_PROVIDER);
        ks.load(null);
        KeyManagerFactory kmf =
                KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
        kmf.init(ks, null);
        SSLContext ssl = SSLContext.getInstance("TLS");
        ssl.init(kmf.getKeyManagers(), null, new SecureRandom());
        return ssl;
    }

    private static void deleteLegacyKey() throws Exception {
        KeyStore ks = KeyStore.getInstance(KEYSTORE_PROVIDER);
        ks.load(null);
        if (ks.containsAlias(LEGACY_ALIAS)) {
            ks.deleteEntry(LEGACY_ALIAS);
            Log.i(TAG, "deleted legacy TLS key " + LEGACY_ALIAS);
        }
    }

    private static X509Certificate loadValid() throws Exception {
        KeyStore ks = KeyStore.getInstance(KEYSTORE_PROVIDER);
        ks.load(null);
        Certificate cert = ks.getCertificate(ALIAS);
        if (cert instanceof X509Certificate) {
            X509Certificate x509 = (X509Certificate) cert;
            if (x509.getNotAfter().getTime() > System.currentTimeMillis() + REFRESH_BEFORE_MS) {
                return x509;
            }
            ks.deleteEntry(ALIAS);
        } else if (cert != null) {
            ks.deleteEntry(ALIAS);
        }
        return null;
    }

    private static void generate(Context appContext, String deviceId) throws Exception {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            generateEc(appContext, deviceId);
        } else {
            // API 21-22: AndroidKeyStore has no EC support, use RSA-2048.
            generateRsa(appContext, deviceId);
        }
        Log.i(TAG, "generated new TLS server certificate");
    }

    private static void generateEc(Context appContext, String deviceId) throws Exception {
        Calendar notBefore = Calendar.getInstance();
        notBefore.add(Calendar.DAY_OF_MONTH, -1);
        Calendar notAfter = Calendar.getInstance();
        notAfter.add(Calendar.YEAR, VALIDITY_YEARS);
        KeyPairGenerator kpg = KeyPairGenerator.getInstance("EC", KEYSTORE_PROVIDER);
        // KeyGenParameterSpec/KeyProperties need API 23+; this method is only
        // called on those versions (see generate()).
        kpg.initialize(new android.security.keystore.KeyGenParameterSpec.Builder(
                        ALIAS,
                        android.security.keystore.KeyProperties.PURPOSE_SIGN
                                | android.security.keystore.KeyProperties.PURPOSE_VERIFY)
                .setAlgorithmParameterSpec(new java.security.spec.ECGenParameterSpec("secp256r1"))
                // TLS signs a digest produced by Conscrypt, so the key must
                // allow raw (DIGEST_NONE) signing.
                .setDigests(android.security.keystore.KeyProperties.DIGEST_NONE)
                .setCertificateSubject(new X500Principal("CN=SawtPlus " + shortId(deviceId)))
                .setCertificateSerialNumber(new BigInteger(64, new SecureRandom()))
                .setCertificateNotBefore(notBefore.getTime())
                .setCertificateNotAfter(notAfter.getTime())
                .build());
        kpg.generateKeyPair();
    }

    private static void generateRsa(Context appContext, String deviceId) throws Exception {
        Calendar notBefore = Calendar.getInstance();
        notBefore.add(Calendar.DAY_OF_MONTH, -1);
        Calendar notAfter = Calendar.getInstance();
        notAfter.add(Calendar.YEAR, VALIDITY_YEARS);
        KeyPairGenerator kpg = KeyPairGenerator.getInstance("RSA", KEYSTORE_PROVIDER);
        kpg.initialize(new android.security.KeyPairGeneratorSpec.Builder(appContext)
                .setAlias(ALIAS)
                .setSubject(new X500Principal("CN=SawtPlus " + shortId(deviceId)))
                .setSerialNumber(new BigInteger(64, new SecureRandom()))
                .setStartDate(notBefore.getTime())
                .setEndDate(notAfter.getTime())
                .build());
        kpg.generateKeyPair();
    }

    private static String shortId(String deviceId) {
        if (deviceId == null || deviceId.isEmpty()) {
            return "tls";
        }
        return deviceId.substring(0, Math.min(8, deviceId.length()));
    }

    private static String toHex(byte[] bytes) {
        StringBuilder sb = new StringBuilder(bytes.length * 2);
        for (byte b : bytes) {
            sb.append(String.format("%02x", b));
        }
        return sb.toString();
    }
}
