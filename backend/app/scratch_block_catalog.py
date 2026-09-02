from __future__ import annotations

from dataclasses import dataclass
from types import MappingProxyType
from typing import Literal, Mapping

BlockShape = Literal['hat', 'stack', 'cap', 'reporter', 'boolean']
InputKind = Literal['number', 'text', 'angle', 'color', 'boolean', 'any']


@dataclass(frozen=True)
class BlockContract:
    shape: BlockShape
    targets: frozenset[str]
    fields: frozenset[str]
    required_fields: frozenset[str]
    resolved_fields: frozenset[str]
    inputs: Mapping[str, InputKind]
    required_inputs: frozenset[str]
    substacks: frozenset[str]
    required_substacks: frozenset[str]


_BOTH = frozenset({'stage', 'sprite'})
_SPRITE = frozenset({'sprite'})
_STAGE = frozenset({'stage'})


def _block(
    shape: BlockShape,
    targets: frozenset[str] = _BOTH,
    *,
    fields: tuple[str, ...] = (),
    resolved_fields: tuple[str, ...] = (),
    optional_fields: tuple[str, ...] = (),
    inputs: tuple[tuple[str, InputKind], ...] = (),
    optional_inputs: tuple[str, ...] = (),
    substacks: tuple[str, ...] = (),
    optional_substacks: tuple[str, ...] = (),
) -> BlockContract:
    all_fields = frozenset((*fields, *resolved_fields))
    all_inputs = MappingProxyType(dict(inputs))
    all_substacks = frozenset(substacks)
    return BlockContract(
        shape=shape,
        targets=targets,
        fields=all_fields,
        required_fields=all_fields.difference(optional_fields),
        resolved_fields=frozenset(resolved_fields),
        inputs=all_inputs,
        required_inputs=frozenset(all_inputs).difference(optional_inputs),
        substacks=all_substacks,
        required_substacks=all_substacks.difference(optional_substacks),
    )


# This mirrors frontend/src/ai-blocks/block-catalog.ts. The backend validates
# model output against it before ProgramIR can reach the browser compiler.
BLOCK_CONTRACTS: Mapping[str, BlockContract] = MappingProxyType({
    'event_whenflagclicked': _block('hat'),
    'event_whenkeypressed': _block('hat', fields=('KEY_OPTION',)),
    'event_whenthisspriteclicked': _block('hat', _SPRITE),
    'event_whenstageclicked': _block('hat', _STAGE),
    'event_whenbackdropswitchesto': _block('hat', fields=('BACKDROP',)),
    'event_whengreaterthan': _block('hat', fields=('WHENGREATERTHANMENU',), inputs=(('VALUE', 'number'),)),
    'event_whenbroadcastreceived': _block('hat', resolved_fields=('BROADCAST_OPTION',)),
    'event_broadcast': _block('stack', inputs=(('BROADCAST_INPUT', 'text'),)),
    'event_broadcastandwait': _block('stack', inputs=(('BROADCAST_INPUT', 'text'),)),

    'motion_movesteps': _block('stack', _SPRITE, inputs=(('STEPS', 'number'),)),
    'motion_turnright': _block('stack', _SPRITE, inputs=(('DEGREES', 'number'),)),
    'motion_turnleft': _block('stack', _SPRITE, inputs=(('DEGREES', 'number'),)),
    'motion_goto': _block('stack', _SPRITE, inputs=(('TO', 'text'),)),
    'motion_gotoxy': _block('stack', _SPRITE, inputs=(('X', 'number'), ('Y', 'number'))),
    'motion_glideto': _block('stack', _SPRITE, inputs=(('SECS', 'number'), ('TO', 'text'))),
    'motion_glidesecstoxy': _block('stack', _SPRITE, inputs=(('SECS', 'number'), ('X', 'number'), ('Y', 'number'))),
    'motion_pointindirection': _block('stack', _SPRITE, inputs=(('DIRECTION', 'angle'),)),
    'motion_pointtowards': _block('stack', _SPRITE, inputs=(('TOWARDS', 'text'),)),
    'motion_changexby': _block('stack', _SPRITE, inputs=(('DX', 'number'),)),
    'motion_setx': _block('stack', _SPRITE, inputs=(('X', 'number'),)),
    'motion_changeyby': _block('stack', _SPRITE, inputs=(('DY', 'number'),)),
    'motion_sety': _block('stack', _SPRITE, inputs=(('Y', 'number'),)),
    'motion_ifonedgebounce': _block('stack', _SPRITE),
    'motion_setrotationstyle': _block('stack', _SPRITE, fields=('STYLE',)),
    'motion_xposition': _block('reporter', _SPRITE),
    'motion_yposition': _block('reporter', _SPRITE),
    'motion_direction': _block('reporter', _SPRITE),

    'looks_sayforsecs': _block('stack', _SPRITE, inputs=(('MESSAGE', 'text'), ('SECS', 'number'))),
    'looks_say': _block('stack', _SPRITE, inputs=(('MESSAGE', 'text'),)),
    'looks_thinkforsecs': _block('stack', _SPRITE, inputs=(('MESSAGE', 'text'), ('SECS', 'number'))),
    'looks_think': _block('stack', _SPRITE, inputs=(('MESSAGE', 'text'),)),
    'looks_switchcostumeto': _block('stack', _SPRITE, inputs=(('COSTUME', 'text'),)),
    'looks_nextcostume': _block('stack', _SPRITE),
    'looks_switchbackdropto': _block('stack', inputs=(('BACKDROP', 'text'),)),
    'looks_nextbackdrop': _block('stack'),
    'looks_changesizeby': _block('stack', _SPRITE, inputs=(('CHANGE', 'number'),)),
    'looks_setsizeto': _block('stack', _SPRITE, inputs=(('SIZE', 'number'),)),
    'looks_changeeffectby': _block('stack', fields=('EFFECT',), inputs=(('CHANGE', 'number'),)),
    'looks_seteffectto': _block('stack', fields=('EFFECT',), inputs=(('VALUE', 'number'),)),
    'looks_cleargraphiceffects': _block('stack'),
    'looks_show': _block('stack', _SPRITE),
    'looks_hide': _block('stack', _SPRITE),
    'looks_gotofrontback': _block('stack', _SPRITE, fields=('FRONT_BACK',)),
    'looks_goforwardbackwardlayers': _block('stack', _SPRITE, fields=('FORWARD_BACKWARD',), inputs=(('NUM', 'number'),)),
    'looks_costumenumbername': _block('reporter', _SPRITE, fields=('NUMBER_NAME',)),
    'looks_backdropnumbername': _block('reporter', fields=('NUMBER_NAME',)),
    'looks_size': _block('reporter', _SPRITE),

    'sound_playuntildone': _block('stack', inputs=(('SOUND_MENU', 'text'),)),
    'sound_play': _block('stack', inputs=(('SOUND_MENU', 'text'),)),
    'sound_stopallsounds': _block('stack'),
    'sound_changeeffectby': _block('stack', fields=('EFFECT',), inputs=(('VALUE', 'number'),)),
    'sound_seteffectto': _block('stack', fields=('EFFECT',), inputs=(('VALUE', 'number'),)),
    'sound_cleareffects': _block('stack'),
    'sound_changevolumeby': _block('stack', inputs=(('VOLUME', 'number'),)),
    'sound_setvolumeto': _block('stack', inputs=(('VOLUME', 'number'),)),
    'sound_volume': _block('reporter'),

    'control_wait': _block('stack', inputs=(('DURATION', 'number'),)),
    'control_repeat': _block('stack', inputs=(('TIMES', 'number'),), substacks=('SUBSTACK',)),
    'control_forever': _block('cap', substacks=('SUBSTACK',)),
    'control_if': _block('stack', inputs=(('CONDITION', 'boolean'),), substacks=('SUBSTACK',)),
    'control_if_else': _block('stack', inputs=(('CONDITION', 'boolean'),), substacks=('SUBSTACK', 'SUBSTACK2')),
    'control_wait_until': _block('stack', inputs=(('CONDITION', 'boolean'),)),
    'control_repeat_until': _block('stack', inputs=(('CONDITION', 'boolean'),), substacks=('SUBSTACK',)),
    'control_stop': _block('cap', fields=('STOP_OPTION',)),
    'control_start_as_clone': _block('hat', _SPRITE),
    'control_create_clone_of': _block('stack', inputs=(('CLONE_OPTION', 'text'),)),
    'control_delete_this_clone': _block('cap', _SPRITE),

    'sensing_touchingobject': _block('boolean', _SPRITE, inputs=(('TOUCHINGOBJECTMENU', 'text'),)),
    'sensing_touchingcolor': _block('boolean', _SPRITE, inputs=(('COLOR', 'color'),)),
    'sensing_coloristouchingcolor': _block('boolean', _SPRITE, inputs=(('COLOR', 'color'), ('COLOR2', 'color'))),
    'sensing_distanceto': _block('reporter', _SPRITE, inputs=(('DISTANCETOMENU', 'text'),)),
    'sensing_askandwait': _block('stack', inputs=(('QUESTION', 'text'),)),
    'sensing_answer': _block('reporter'),
    'sensing_keypressed': _block('boolean', inputs=(('KEY_OPTION', 'text'),)),
    'sensing_mousedown': _block('boolean'),
    'sensing_mousex': _block('reporter'),
    'sensing_mousey': _block('reporter'),
    'sensing_setdragmode': _block('stack', _SPRITE, fields=('DRAG_MODE',)),
    'sensing_loudness': _block('reporter'),
    'sensing_timer': _block('reporter'),
    'sensing_resettimer': _block('stack'),
    'sensing_current': _block('reporter', fields=('CURRENTMENU',)),
    'sensing_dayssince2000': _block('reporter'),
    'sensing_username': _block('reporter'),

    'operator_add': _block('reporter', inputs=(('NUM1', 'number'), ('NUM2', 'number'))),
    'operator_subtract': _block('reporter', inputs=(('NUM1', 'number'), ('NUM2', 'number'))),
    'operator_multiply': _block('reporter', inputs=(('NUM1', 'number'), ('NUM2', 'number'))),
    'operator_divide': _block('reporter', inputs=(('NUM1', 'number'), ('NUM2', 'number'))),
    'operator_random': _block('reporter', inputs=(('FROM', 'number'), ('TO', 'number'))),
    'operator_gt': _block('boolean', inputs=(('OPERAND1', 'any'), ('OPERAND2', 'any'))),
    'operator_lt': _block('boolean', inputs=(('OPERAND1', 'any'), ('OPERAND2', 'any'))),
    'operator_equals': _block('boolean', inputs=(('OPERAND1', 'any'), ('OPERAND2', 'any'))),
    'operator_and': _block('boolean', inputs=(('OPERAND1', 'boolean'), ('OPERAND2', 'boolean'))),
    'operator_or': _block('boolean', inputs=(('OPERAND1', 'boolean'), ('OPERAND2', 'boolean'))),
    'operator_not': _block('boolean', inputs=(('OPERAND', 'boolean'),)),
    'operator_join': _block('reporter', inputs=(('STRING1', 'text'), ('STRING2', 'text'))),
    'operator_letter_of': _block('reporter', inputs=(('LETTER', 'number'), ('STRING', 'text'))),
    'operator_length': _block('reporter', inputs=(('STRING', 'text'),)),
    'operator_contains': _block('boolean', inputs=(('STRING1', 'text'), ('STRING2', 'text'))),
    'operator_mod': _block('reporter', inputs=(('NUM1', 'number'), ('NUM2', 'number'))),
    'operator_round': _block('reporter', inputs=(('NUM', 'number'),)),
    'operator_mathop': _block('reporter', fields=('OPERATOR',), inputs=(('NUM', 'number'),)),

    'data_variable': _block('reporter', resolved_fields=('VARIABLE',)),
    'data_setvariableto': _block('stack', resolved_fields=('VARIABLE',), inputs=(('VALUE', 'text'),)),
    'data_changevariableby': _block('stack', resolved_fields=('VARIABLE',), inputs=(('VALUE', 'number'),)),
    'data_showvariable': _block('stack', resolved_fields=('VARIABLE',)),
    'data_hidevariable': _block('stack', resolved_fields=('VARIABLE',)),
    'data_listcontents': _block('reporter', resolved_fields=('LIST',)),
    'data_addtolist': _block('stack', resolved_fields=('LIST',), inputs=(('ITEM', 'text'),)),
    'data_deleteoflist': _block('stack', resolved_fields=('LIST',), inputs=(('INDEX', 'text'),)),
    'data_deletealloflist': _block('stack', resolved_fields=('LIST',)),
    'data_insertatlist': _block('stack', resolved_fields=('LIST',), inputs=(('ITEM', 'text'), ('INDEX', 'text'))),
    'data_replaceitemoflist': _block('stack', resolved_fields=('LIST',), inputs=(('INDEX', 'text'), ('ITEM', 'text'))),
    'data_itemoflist': _block('reporter', resolved_fields=('LIST',), inputs=(('INDEX', 'text'),)),
    'data_itemnumoflist': _block('reporter', resolved_fields=('LIST',), inputs=(('ITEM', 'text'),)),
    'data_lengthoflist': _block('reporter', resolved_fields=('LIST',)),
    'data_listcontainsitem': _block('boolean', resolved_fields=('LIST',), inputs=(('ITEM', 'text'),)),
    'data_showlist': _block('stack', resolved_fields=('LIST',)),
    'data_hidelist': _block('stack', resolved_fields=('LIST',)),
})
