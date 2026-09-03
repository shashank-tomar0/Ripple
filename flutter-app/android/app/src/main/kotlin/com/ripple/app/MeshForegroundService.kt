package com.ripple.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Intent
import android.os.Build
import android.os.IBinder
import java.io.File

class MeshForegroundService : Service() {

    companion object {
        const val CHANNEL_ID = "ripple_mesh_channel"
        const val NOTIFICATION_ID = 1001
        const val ACTION_START = "com.ripple.app.START_MESH"
        const val ACTION_STOP = "com.ripple.app.STOP_MESH"
    }

    private var daemonProcess: java.lang.Process? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_START -> startMeshDaemon()
            ACTION_STOP -> stopSelf()
        }
        return START_STICKY
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "Ripple Mesh",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Ripple mesh network is active"
                setShowBadge(false)
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun startMeshDaemon() {
        // Build notification
        val notification = Notification.Builder(this, CHANNEL_ID)
            .setContentTitle("Ripple Mesh Active")
            .setContentText("Relaying messages for the mesh network")
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setOngoing(true)
            .setPriority(Notification.PRIORITY_LOW)
            .build()

        startForeground(NOTIFICATION_ID, notification)

        // Start the Go daemon process
        try {
            val dataDir = File(filesDir, ".ripple")
            dataDir.mkdirs()

            val builder = ProcessBuilder(
                applicationInfo.nativeLibraryDir + "/librippled.so",
                "-port", "9000",
                "-wsport", "9876",
                "-nick", android.os.Build.MODEL,
                "-data", dataDir.absolutePath,
                "-db", File(dataDir, "ripple.db").absolutePath
            )
            builder.directory(filesDir)
            daemonProcess = builder.start()
        } catch (e: Exception) {
            e.printStackTrace()
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        daemonProcess?.destroy()
        super.onDestroy()
    }
}
