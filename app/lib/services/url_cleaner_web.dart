import 'dart:js_interop';

@JS('window.history.replaceState')
external void _replaceState(JSAny? data, String unused, String url);

void clearAuthQueryParams() {
  final uri = Uri.base;
  if (uri.queryParameters.containsKey('token') ||
      uri.queryParameters.containsKey('error') ||
      uri.queryParameters.containsKey('identity')) {
    final cleanParams = Map<String, String>.from(uri.queryParameters)
      ..remove('token')
      ..remove('error')
      ..remove('identity');
    final newUri = Uri(
      path: uri.path.isEmpty ? '/' : uri.path,
      queryParameters: cleanParams.isEmpty ? null : cleanParams,
      fragment: uri.fragment.isEmpty ? null : uri.fragment,
    );
    _replaceState(null, '', newUri.toString());
  }
}
