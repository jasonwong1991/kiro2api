# -*- coding: utf-8 -*-

"""
邮箱前缀生成器 - 使用音节算法生成类似英文人名的字符串
"""

import random


def gen_name() -> str:
    """用音节算法生成类似英文人名的字符串"""
    # 首字母大写的起始音节
    first_syllables = ['Ca', 'Da', 'Ja', 'Ma', 'Sa', 'Ta', 'La', 'Ra', 'Na', 'Ka',
                       'Mi', 'Li', 'Ri', 'Ti', 'Si', 'Ni', 'Ki', 'Di', 'Bi', 'Vi',
                       'An', 'En', 'In', 'On', 'Al', 'El', 'Ol', 'Ar', 'Er', 'Or',
                       'Br', 'Ch', 'Cl', 'Cr', 'Dr', 'Fr', 'Gr', 'Pr', 'Tr', 'St',
                       'Sh', 'Th', 'Wh', 'Bl', 'Fl', 'Gl', 'Pl', 'Sl', 'Sp', 'Sw']

    # 中间音节
    mid_syllables = ['an', 'en', 'in', 'on', 'un', 'ar', 'er', 'ir', 'or', 'ur',
                     'al', 'el', 'il', 'ol', 'ul', 'am', 'em', 'im', 'om', 'um',
                     'ay', 'ey', 'oy', 'ly', 'ry', 'ny', 'dy', 'ty', 'sy', 'vy',
                     'la', 'le', 'li', 'lo', 'ra', 're', 'ri', 'ro', 'na', 'ne',
                     'da', 'de', 'di', 'do', 'ta', 'te', 'ti', 'to', 'sa', 'se',
                     'va', 've', 'vi', 'vo', 'ka', 'ke', 'ki', 'ko', 'ma', 'me']

    # 结尾音节
    end_syllables = ['son', 'ton', 'don', 'man', 'ley', 'ner', 'ter', 'der', 'ber', 'ger',
                     'ez', 'es', 'is', 'os', 'us', 'ia', 'ie', 'io', 'ey', 'ay',
                     'er', 'ar', 'or', 'an', 'en', 'in', 'on', 'el', 'al', 'il',
                     'ck', 'th', 'ng', 'rd', 'ld', 'nd', 'nt', 'rt', 'st', 'tt',
                     'ly', 'ry', 'ny', 'dy', 'ty', 'sy', 'vy', 'ky', 'my', 'py']

    # 随机选择音节数量 (2-3个音节)
    syllable_count = random.randint(1, 2)

    name = random.choice(first_syllables)
    for _ in range(syllable_count):
        name += random.choice(mid_syllables)
    name += random.choice(end_syllables)

    return name


def gen_email_prefix() -> str:
    """生成人名格式的邮箱前缀，如 CameronRuiz3350"""
    first_name = gen_name()
    last_name = gen_name()
    number = random.randint(1000, 9999)
    return f"{first_name}{last_name}{number}"
