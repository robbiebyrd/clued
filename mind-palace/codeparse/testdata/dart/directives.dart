// Directives: imports with prefixes, combinators and deferred loading,
// conditional imports, exports and parts.
library;

import 'dart:async';
import 'dart:convert' show jsonDecode, jsonEncode;
import 'dart:io' as io hide Platform;
import 'package:http/http.dart' as http;
import 'package:path/path.dart' as p show join, basename;
import 'package:demo/src/shapes.dart';
import '../util/strings.dart' deferred as strings;
import 'src/storage_stub.dart'
    if (dart.library.io) 'src/storage_io.dart'
    if (dart.library.js_interop) 'src/storage_web.dart';

export 'src/shapes.dart' show Shape, Circle hide Square;
export 'src/colours.dart';

part 'directives_impl.dart';
part 'directives.g.dart';

/// Fetches [url] and decodes its JSON body.
Future<Map<String, dynamic>> fetchJson(Uri url) async {
  final response = await http.get(url);
  if (response.statusCode != 200) {
    throw io.HttpException('status ${response.statusCode}', uri: url);
  }
  return jsonDecode(response.body) as Map<String, dynamic>;
}

/// Saves [data] under [name] in [dir].
Future<void> save(String dir, String name, Object data) async {
  final file = io.File(p.join(dir, name));
  await file.writeAsString(jsonEncode(data));
  await strings.loadLibrary();
  print(strings.shout(p.basename(file.path)));
  openStorage().write(name, data);
}

StreamSubscription<Shape> watch(Stream<Shape> shapes) =>
    shapes.listen((s) => Circle(s.area).printMe());
