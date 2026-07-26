// Package services provides the communication and utility layers for Ripple.
// FilePickerService abstracts file selection across mobile/desktop platforms.
library;

import 'dart:io';
import 'dart:typed_data';
import 'package:flutter/foundation.dart';
import 'package:file_picker/file_picker.dart' as fp;
import 'package:mime/mime.dart' as mime;

/// Metadata about a file picked by the user.
class FilePickerResult {
  final String path;
  final String name;
  final int size;
  final String mimeType;
  final Uint8List? bytes;

  FilePickerResult({
    required this.path,
    required this.name,
    required this.size,
    this.mimeType = 'application/octet-stream',
    this.bytes,
  });
}

/// Abstract interface for platform file picking.
///
/// Allows swapping implementations:
///   - [LocalFilePickerService] wraps the `file_picker` package for real use
///   - A mock implementation can be used for unit tests
abstract class FilePickerService {
  /// Opens a system file picker for any file type.
  Future<FilePickerResult?> pickFile();

  /// Opens a system image picker (or file picker filtered to images).
  Future<FilePickerResult?> pickImage();

  /// Reads a file's bytes from the given [path].
  Future<List<int>> readFileBytes(String path);
}

/// Real implementation wrapping the `file_picker` and `mime` packages.
///
/// On mobile uses native file/image picker dialogs.
/// On desktop falls back to the system file dialog.
class LocalFilePickerService implements FilePickerService {
  @override
  Future<FilePickerResult?> pickFile() async {
    try {
      final result = await fp.FilePicker.platform.pickFiles();
      if (result == null || result.files.isEmpty) return null;
      return _buildResult(result.files.first);
    } catch (e) {
      debugPrint('FilePickerService.pickFile error: $e');
      return null;
    }
  }

  @override
  Future<FilePickerResult?> pickImage() async {
    try {
      final result = await fp.FilePicker.platform.pickFiles(
        type: fp.FileType.image,
      );
      if (result == null || result.files.isEmpty) return null;
      return _buildResult(result.files.first);
    } catch (e) {
      debugPrint('FilePickerService.pickImage error: $e');
      return null;
    }
  }

  @override
  Future<List<int>> readFileBytes(String path) async {
    final file = File(path);
    return file.readAsBytes();
  }

  FilePickerResult _buildResult(fp.PlatformFile file) {
    final path = file.path ?? '';
    final bytes = file.bytes;
    final detectedType = path.isNotEmpty && path.contains('.')
        ? (mime.lookupMimeType(path) ?? 'application/octet-stream')
        : 'application/octet-stream';

    return FilePickerResult(
      path: path,
      name: file.name,
      size: file.size,
      mimeType: detectedType,
      bytes: bytes,
    );
  }
}
