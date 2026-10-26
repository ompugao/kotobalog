import 'package:flutter/material.dart';

class User {
  final String id;
  final String email;
  final String displayName;
  final String avatarUrl;

  const User({
    required this.id,
    required this.email,
    required this.displayName,
    required this.avatarUrl,
  });

  factory User.fromJson(Map<String, dynamic> json) {
    return User(
      id: json['id'] as String? ?? '',
      email: json['email'] as String? ?? '',
      displayName: json['display_name'] as String? ?? '',
      avatarUrl: json['avatar_url'] as String? ?? '',
    );
  }
}

class NotebookMember {
  final String userId;
  final String displayName;
  final String email;
  final String role;

  const NotebookMember({
    required this.userId,
    required this.displayName,
    required this.email,
    required this.role,
  });

  factory NotebookMember.fromJson(Map<String, dynamic> json) {
    return NotebookMember(
      userId: json['user_id'] as String? ?? '',
      displayName: json['display_name'] as String? ?? '',
      email: json['email'] as String? ?? '',
      role: json['role'] as String? ?? 'member',
    );
  }
}

class Notebook {
  final String id;
  final String childName;
  final String birthDate; // YYYY-MM-DD
  final String themeColor;
  final String iconEmoji;
  final List<NotebookMember> members;

  const Notebook({
    required this.id,
    required this.childName,
    required this.birthDate,
    required this.themeColor,
    required this.iconEmoji,
    required this.members,
  });

  factory Notebook.fromJson(Map<String, dynamic> json) {
    final membersList = (json['members'] as List<dynamic>? ?? [])
        .map((m) => NotebookMember.fromJson(m as Map<String, dynamic>))
        .toList();
    return Notebook(
      id: json['id'] as String? ?? '',
      childName: json['child_name'] as String? ?? '',
      birthDate: json['birth_date'] as String? ?? '',
      themeColor: json['theme_color'] as String? ?? '#FF8A65',
      iconEmoji: json['icon_emoji'] as String? ?? '👶',
      members: membersList,
    );
  }

  /// 「はるくんのことば帳」のような表示タイトル
  String get title => '$childNameのことば帳';

  Color get color {
    final hex = themeColor.replaceFirst('#', '');
    if (hex.length == 6) {
      final val = int.tryParse('FF$hex', radix: 16);
      if (val != null) return Color(val);
    }
    return const Color(0xFFFF8A65);
  }

  DateTime? get parsedBirthDate {
    return DateTime.tryParse(birthDate);
  }

  /// 指定された日時 (`targetDate`) 時点での月齢・年齢ラベル（例: "1歳5ヶ月"、"生後8ヶ月"）を計算する
  String ageLabelAt(DateTime targetDate) {
    final birth = parsedBirthDate;
    if (birth == null) return '';

    final target = DateTime(targetDate.year, targetDate.month, targetDate.day);
    final b = DateTime(birth.year, birth.month, birth.day);
    if (target.isBefore(b)) {
      return '誕生前';
    }

    int years = target.year - b.year;
    int months = target.month - b.month;
    int days = target.day - b.day;

    if (days < 0) {
      months -= 1;
    }
    if (months < 0) {
      years -= 1;
      months += 12;
    }

    if (years == 0 && months == 0) {
      final diffDays = target.difference(b).inDays;
      return '生後$diffDays日';
    } else if (years == 0) {
      return '生後$monthsヶ月';
    } else if (months == 0) {
      return '$years歳0ヶ月';
    } else {
      return '$years歳$monthsヶ月';
    }
  }
}
